package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/codahale/hdrhistogram"
	"github.com/olekukonko/tablewriter"
)

// Summary contains the results of a Benchmark run.
type Summary struct {
	Connections      uint64
	RequestRate      float64
	SuccessTotal     uint64
	ErrorTotal       uint64
	TimeElapsed      time.Duration
	SuccessHistogram *hdrhistogram.Histogram
	Throughput       float64
	AvgRequestTime   float64
	Errors           map[string]int
	TicksTimely      uint64
	TicksTimelyRatio float64
	SendsTimely      uint64
	SendsTimelyRatio float64
	OutputJson       bool
}

// Struct and functions for sorting errors
type Error struct {
	ErrorCode string
	Count     int
}
type ErrorList []Error

func (p ErrorList) Len() int           { return len(p) }
func (p ErrorList) Less(i, j int) bool { return p[i].Count < p[j].Count }
func (p ErrorList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// String returns a stringified version of the Summary.
func (s *Summary) String() string {
	requestTotal := s.SuccessTotal + s.ErrorTotal
	successRate := 0.
	if requestTotal > 0 {
		successRate = float64(s.SuccessTotal) / float64(requestTotal) * 100
	}

	var outputBuffer bytes.Buffer

	fmt.Fprintf(&outputBuffer,
		"\n{SuccessRate: %.2f%%, Throughput: %.2f req/s, AvgRequestTime: %.2f ms, Connections: %d, RequestRate: %.0f, RequestTotal: %d, SuccessTotal: %d, ErrorTotal: %d, TimeElapsed: %s}\n",
		successRate, s.Throughput, s.AvgRequestTime, s.Connections, s.RequestRate, requestTotal, s.SuccessTotal, s.ErrorTotal, s.TimeElapsed)

	if s.OutputJson {
		// Serializing Summary object into JSON
		jsonString, err := json.Marshal(s)
		outputBuffer.WriteString("\nJson Output: ")
		outputBuffer.WriteString(string(jsonString) + "\n")
		if err != nil {
			outputBuffer.WriteString("Error creating Json\n")
		}
	}

	metricsTable := tablewriter.NewWriter(&outputBuffer)
	metricsTable.SetHeader([]string{"Metric", "Absolute", "Percentage %"})

	//Printing metric data as a table
	metricsTable.Append([]string{"Total Requests", strconv.FormatUint(requestTotal, 10), ""})
	metricsTable.Append([]string{"Successful Requests", strconv.FormatUint(s.SuccessTotal, 10), strconv.FormatFloat(successRate, 'f', 2, 64)})
	metricsTable.Append([]string{"Failed Requests", strconv.FormatUint(s.ErrorTotal, 10), strconv.FormatFloat(100-successRate, 'f', 2, 64)})
	metricsTable.Append([]string{"Time Elapsed (sec)", strconv.FormatFloat(s.TimeElapsed.Seconds(), 'f', 2, 64), ""})
	metricsTable.Append([]string{"Request Rate (req/sec)", strconv.FormatFloat(s.RequestRate, 'f', 2, 64), ""})
	metricsTable.Append([]string{"Throughput (req/sec)", strconv.FormatFloat(s.Throughput, 'f', 2, 64), ""})
	metricsTable.Append([]string{"AvgRequestTime (ms)", strconv.FormatFloat(s.AvgRequestTime, 'f', 2, 64), ""})
	metricsTable.Append([]string{"Timely Ticks", strconv.FormatUint(s.TicksTimely, 10), strconv.FormatFloat(s.TicksTimelyRatio, 'f', 2, 64)})
	metricsTable.Append([]string{"Timely Sends", strconv.FormatUint(s.SendsTimely, 10), strconv.FormatFloat(s.SendsTimelyRatio, 'f', 2, 64)})

	//Printing error results as a table
	//Laying out headers and values
	errorTable := tablewriter.NewWriter(&outputBuffer)
	errorTable.SetHeader([]string{"Error", "Absolute", "Percentage %"})

	//Sorting errors by highest count
	el := make(ErrorList, len(s.Errors))
	i := 0
	for code, count := range s.Errors {
		el[i] = Error{code, count}
		i++
	}
	sort.Sort(sort.Reverse(el)) //Sort in descending order

	//Loop through each Error and print count
	for _, err := range el {
		percentage := float64(err.Count) / float64(requestTotal) * 100
		errorTable.Append([]string{err.ErrorCode, strconv.Itoa(err.Count), strconv.FormatFloat(percentage, 'f', 2, 64)})
	}

	outputBuffer.WriteString("\n")
	metricsTable.Render()

	if el.Len() > 0 {
		outputBuffer.WriteString("\n")
		errorTable.Render()
	}

	return outputBuffer.String()
}

// GenerateLatencyDistribution generates a text file containing the specified
// latency distribution in a format plottable by
// http://hdrhistogram.github.io/HdrHistogram/plotFiles.html. Percentiles is a
// list of percentiles to include, e.g. 10.0, 50.0, 99.0, 99.99, etc. If
// percentiles is nil, it defaults to a logarithmic percentile scale. If a
// request rate was specified for the benchmark, this will also generate an
// uncorrected distribution file which does not account for coordinated
// omission.
func (s *Summary) GenerateLatencyDistribution(percentiles Percentiles, file string) error {
	return generateLatencyDistribution(s.SuccessHistogram, nil, s.RequestRate, percentiles, file)
}

func generateLatencyDistribution(histogram, unHistogram *hdrhistogram.Histogram, requestRate float64, percentiles Percentiles, file string) error {
	if percentiles == nil {
		percentiles = Logarithmic
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()

	f.WriteString("Value    Percentile    TotalCount    1/(1-Percentile)\n\n")
	for _, percentile := range percentiles {
		value := float64(histogram.ValueAtQuantile(percentile)) / 1000000
		_, err := f.WriteString(fmt.Sprintf("%f    %f        %d            %f\n",
			value, percentile/100, 0, 1/(1-(percentile/100))))
		if err != nil {
			return err
		}
	}

	// Generate uncorrected distribution.
	if requestRate > 0 && unHistogram != nil {
		f, err := os.Create(file + ".uncorrected")
		if err != nil {
			return err
		}
		defer f.Close()

		f.WriteString("Value    Percentile    TotalCount    1/(1-Percentile)\n\n")
		for _, percentile := range percentiles {
			value := float64(unHistogram.ValueAtQuantile(percentile)) / 1000000
			_, err := f.WriteString(fmt.Sprintf("%f    %f        %d            %f\n",
				value, percentile/100, 0, 1/(1-(percentile/100))))
			if err != nil {
				return err
			}
		}
	}

	return nil
}
