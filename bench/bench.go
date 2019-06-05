/*
Package bench provides a generic framework for performing latency benchmarks.
*/
package bench

import (
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"fmt"
	"log"

	"github.com/codahale/hdrhistogram"
)

const (
	minRecordableLatencyNS = 1000000
	maxRecordableLatencyNS = 100000000000
	sigFigs                = 5
)

// RequesterFactory creates new Requesters.
type RequesterFactory interface {
	// GetRequester returns a new Requester, called for each Benchmark
	// connection.
	GetRequester(number uint64) Requester
}

// Requester synchronously issues requests for a particular system under test.
type Requester interface {
	// Setup prepares the Requester for benchmarking.
	Setup() error

	// Request performs a synchronous request to the system under test.
	Request() error

	// Teardown is called upon benchmark completion.
	Teardown() error
}

// Benchmark performs a system benchmark by attempting to issue requests at a
// specified rate and capturing the latency distribution. The request rate is
// divided across the number of configured connections.
type Benchmark struct {
	connections      uint64
	requestRate      float64
	duration         time.Duration
	baseLatency      time.Duration
	expectedInterval time.Duration
	successHistogram *hdrhistogram.Histogram
	successTotal     uint64
	errorTotal       uint64
	avgRequestTime   float64
	elapsed          time.Duration
	factory          RequesterFactory
	timelyTicks      uint64
	missedTicks      uint64
	timelySends      uint64
	lateSends        uint64
	errors           map[string]int
}

// NewBenchmark creates a Benchmark which runs a system benchmark using the
// given RequesterFactory. The requestRate argument specifies the number of
// requests per second to issue. This value is divided across the number of
// connections specified, so if requestRate is 50,000 and connections is 10,
// each connection will attempt to issue 5,000 requests per second. A zero
// value disables rate limiting entirely. The duration argument specifies how
// long to run the benchmark.
func NewBenchmark(factory RequesterFactory, requestRate, connections uint64, duration time.Duration, baseLatency time.Duration) *Benchmark {

	if connections == 0 {
		connections = 1
	}

	if requestRate <= 0 {
		log.Panicln("RequestRate must be positive")
	}

	return &Benchmark{
		connections:      connections,
		requestRate:      float64(requestRate),
		duration:         duration,
		baseLatency:      baseLatency,
		expectedInterval: time.Duration(float64(time.Second) / float64(requestRate)),
		successHistogram: hdrhistogram.New(minRecordableLatencyNS, maxRecordableLatencyNS, sigFigs),
		factory:          factory,
		errors:           make(map[string]int)}
}

// Run the benchmark and return a summary of the results. An error is returned
// if something went wrong along the way.
func (b *Benchmark) Run(outputJson bool, forceTightTicker bool) (*Summary, error) {
	var (
		ticker        = make(chan time.Time)
		results       = make(chan int64, 100)
		errors        = make(chan error, 100)
		done          = make(chan struct{})
		stopCollector = make(chan struct{})
		wg            sync.WaitGroup
	)

	// Prepare connection benchmarks
	wg.Add(int(b.connections))
	for i := uint64(0); i < b.connections; i++ {
		i := i
		go func() {
			b.worker(b.factory.GetRequester(i), ticker, results, errors)
			// log.Printf("Worker %d done\n", i)
			wg.Done()
		}()
	}

	// Prepare ticker
	go b.tickerFunc(done, ticker, forceTightTicker)

	// Prepare results collector
	go func() {
		b.collectorFunc(stopCollector, results, errors)
		// log.Println("Collector done")
		wg.Done()
	}()

	// Wait for completion of workers
	wg.Wait()
	// log.Println("Workers have finished")

	wg.Add(1)
	close(stopCollector)
	wg.Wait()

	// log.Println("Collector has finished")

	fmt.Printf("Ticks=%d, TimelyTicks = %d, MissedTicks = %d, %.2f%% good\n", b.timelyTicks+b.missedTicks, b.timelyTicks, b.missedTicks, float64(b.timelyTicks)*100/float64(b.timelyTicks+b.missedTicks))
	fmt.Printf("Sends=%d, TimelySends = %d, LateSends   = %d, %.2f%% good\n", b.timelySends+b.lateSends, b.timelySends, b.lateSends, float64(b.timelySends)*100/float64(b.timelySends+b.lateSends))

	if len(b.errors) > 0 {
		fmt.Println()
		fmt.Println("Errors:")
		for etext, count := range b.errors {
			fmt.Println(count, "=", etext)
		}
		fmt.Println()
	}

	summary := b.summarize(outputJson)
	return summary, nil
}

func (b *Benchmark) collectorFunc(doneCh <-chan struct{}, results <-chan int64, errors <-chan error) {
	var (
		baseLatency    = b.baseLatency.Nanoseconds()
		successTotal   int64
		avgRequestTime float64 // Average latency for processing requests
	)
	for {
		select {
		case sample := <-results:
			successTotal++
			maybePanic(b.successHistogram.RecordValue(sample - baseLatency))
			avgRequestTime = (avgRequestTime*float64(successTotal-1) + float64(sample/1e6)) / float64(successTotal)
		case err := <-errors:
			b.errors[err.Error()]++
		case <-doneCh:
			b.avgRequestTime = avgRequestTime
			return
		}
	}
}

func detectOsTimerResolution() time.Duration {
	bestTimerRes := time.Hour

	for i := 0; i < 10; i++ {
		start := time.Now()
		var timerRes time.Duration
		for {
			timerRes = time.Since(start)
			if timerRes > 0 {
				if timerRes < bestTimerRes {
					bestTimerRes = timerRes
				}
				break
			}
		}
	}

	return bestTimerRes
}

func (b *Benchmark) tickerFunc(doneCh chan<- struct{}, outCh chan<- time.Time, forceTightTicker bool) {
	timerRes := detectOsTimerResolution()
	fmt.Printf("ExpectedInterval = %v, Detected OS timer resolution = %v\n", b.expectedInterval, timerRes)
	if timerRes*3 > b.expectedInterval {
		fmt.Println("WARNING! Detected OS timer resolution may not be sufficient for desired request rate")
	}

	// let other go routines to start running
	time.Sleep(200 * time.Millisecond)

	if !forceTightTicker && b.expectedInterval >= 7*timerRes {
		fmt.Println("Using sleeping ticker")
		b.sleepingTicker(doneCh, outCh)
	} else {
		fmt.Println("Using tight ticker")
		b.tightTicker(doneCh, outCh)
	}
}

func (b *Benchmark) tightTicker(doneCh chan<- struct{}, outCh chan<- time.Time) {
	start := time.Now()
	lastTick := start

	var (
		timelyTicks uint64
		missedTicks uint64
	)

	expectedInterval := b.expectedInterval
	duration := b.duration

	for {
		var thisTick time.Time

		for {
			thisTick = time.Now()
			if thisTick.Sub(lastTick) >= expectedInterval {
				lastTick = lastTick.Add(expectedInterval)
				break
			}
		}

		select {
		case outCh <- thisTick:
			timelyTicks++
		default:
			missedTicks++
		}

		if thisTick.Sub(start) > duration {
			// log.Println("Signaling DONE")
			close(outCh)
			break
		}
	}

	close(doneCh)
	b.elapsed = time.Since(start)

	b.timelyTicks = timelyTicks
	b.missedTicks = missedTicks
}

func (b *Benchmark) sleepingTicker(doneCh chan<- struct{}, outCh chan<- time.Time) {
	completion := time.After(b.duration)

	inCh := time.Tick(b.expectedInterval)

	start := time.Now()

	var (
		timelyTicks uint64
		missedTicks uint64
	)

	// initial tick
	outCh <- start
	timelyTicks++

loop:
	for {
		select {
		case t := <-inCh:
			select {
			case outCh <- t:
				timelyTicks++
			default:
				missedTicks++
			}

		case <-completion:
			// log.Println("Signaling DONE")
			close(outCh)
			break loop
		}
	}

	close(doneCh)
	b.elapsed = time.Since(start)

	b.timelyTicks = timelyTicks
	b.missedTicks = missedTicks
}

func maybePanic(err error) {
	if err != nil {
		log.Panic(err)
	}
}

func (b *Benchmark) worker(requester Requester, ticker <-chan time.Time, results chan<- int64, errors chan<- error) {
	maybePanic(requester.Setup())

	// initialized to 0 by default
	var (
		lateSends    uint64
		timelySends  uint64
		errorTotal   uint64
		successTotal uint64
	)

	for tick := range ticker {
		before := time.Now()
		if before.Sub(tick) >= b.expectedInterval {
			lateSends++
		} else {
			timelySends++
		}

		err := requester.Request()
		latency := time.Since(before).Nanoseconds()
		if err != nil {
			errorTotal++
			errors <- err
		} else {
			// On Linux, sometimes time interval measurement comes back negative, report it as 0
			if latency < 0 {
				latency = 0
			}
			results <- latency
			successTotal++
		}
	}

	atomic.AddUint64(&b.lateSends, lateSends)
	atomic.AddUint64(&b.timelySends, timelySends)
	atomic.AddUint64(&b.errorTotal, errorTotal)
	atomic.AddUint64(&b.successTotal, successTotal)

	err := requester.Teardown()
	if err != nil {
		log.Println("Failure in Teardown:", err)
	}
}

// summarize returns a Summary of the last benchmark run.
func (b *Benchmark) summarize(outputJson bool) *Summary {

	//Checks the list of target errors against the errors found during benchmarking
	formattedErrors := make(map[string]int)
	r := regexp.MustCompile(`Expected 200-response, but got (\d+)`)

	//For every error that was found during benchmarking
	for errorText, count := range b.errors {

		//Use regex to extract error code
		errorCodeMatches := r.FindStringSubmatch(errorText)

		// If the regex extracted an errorCode then use the errorCode as the key
		if len(errorCodeMatches) > 1 {

			//Set the error count
			errorCode := errorCodeMatches[1]
			formattedErrors[errorCode] = count

		} else {
			//If the error doesnt have an errorCode then use the full text as the key
			formattedErrors[errorText] = count
		}
	}

	return &Summary{
		SuccessTotal:     b.successTotal,
		ErrorTotal:       b.errorTotal,
		TimeElapsed:      b.elapsed,
		SuccessHistogram: hdrhistogram.Import(b.successHistogram.Export()),
		Throughput:       float64(b.successTotal+b.errorTotal) / b.elapsed.Seconds(),
		AvgRequestTime:   b.avgRequestTime,
		RequestRate:      b.requestRate,
		Connections:      b.connections,
		Errors:           formattedErrors,
		TicksTimely:      b.timelyTicks,
		TicksTimelyRatio: float64(b.timelyTicks) * 100 / float64(b.timelyTicks+b.missedTicks),
		SendsTimely:      b.timelySends,
		SendsTimelyRatio: float64(b.timelySends) * 100 / float64(b.timelySends+b.lateSends),
		OutputJson:       outputJson,
	}
}
