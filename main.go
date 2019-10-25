package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"time"

	"labench/bench"

	yaml "gopkg.in/yaml.v2"
)

type benchParams struct {
	RequestRatePerSec uint64        `yaml:"RequestRatePerSec"`
	Clients           uint64        `yaml:"Clients"`
	Duration          time.Duration `yaml:"Duration"`
	BaseLatency       time.Duration `yaml:"BaseLatency"`
	RequestTimeout    time.Duration `yaml:"RequestTimeout"`
	ReuseConnections  bool          `yaml:"ReuseConnections"`
	DontLinger        bool          `yaml:"DontLinger"`
	OutputJSON        bool          `yaml:"OutputJSON"`
	TightTicker       bool          `yaml:"TightTicker"`
}

type config struct {
	Params   benchParams         `yaml:",inline"`
	Protocol string              `yaml:"Protocol"`
	Request  WebRequesterFactory `yaml:"Request"`
	Output   string              `yaml:"OutFile"`
}

func maybePanic(err error) {
	if err != nil {
		log.Panic(err)
	}
}

func assert(cond bool, err string) {
	if !cond {
		log.Panic(errors.New(err))
	}
}

func main() {
	configFile := "labench.yaml"
	if len(os.Args) > 1 {
		assert(len(os.Args) == 2, fmt.Sprintf("Usage: %s [config.yaml]\n\tThe default config file name is: %s", os.Args[0], configFile))
		configFile = os.Args[1]
	}

	configBytes, err := ioutil.ReadFile(configFile)
	maybePanic(err)

	var conf config
	err = yaml.Unmarshal(configBytes, &conf)
	maybePanic(err)

	// fmt.Printf("%+v\n", conf)
	fmt.Println("timeStart =", time.Now().UTC().Add(-5*time.Second).Truncate(time.Second))

	if conf.Request.ExpectedHTTPStatusCode == 0 {
		conf.Request.ExpectedHTTPStatusCode = 200
	}

	if conf.Request.HTTPMethod == "" {
		if conf.Request.Body == "" && conf.Request.BodyPath == "" {
			conf.Request.HTTPMethod = http.MethodGet
		} else {
			conf.Request.HTTPMethod = http.MethodPost
		}
	}

	if conf.Protocol == "" {
		conf.Protocol = "HTTP/1.1"
	}

	fmt.Println("Protocol:", conf.Protocol)

	switch conf.Protocol {
	case "HTTP/2":
		initHTTP2Client(conf.Params.RequestTimeout, conf.Params.DontLinger)

	default:
		initHTTPClient(conf.Params.ReuseConnections, conf.Params.RequestTimeout, conf.Params.DontLinger)
	}

	if conf.Params.RequestTimeout == 0 {
		conf.Params.RequestTimeout = 10 * time.Second
	}

	if conf.Params.Clients == 0 {
		clients := conf.Params.RequestRatePerSec * uint64(math.Ceil(conf.Params.RequestTimeout.Seconds()))
		clients += clients / 5 // add 20%
		conf.Params.Clients = clients
		fmt.Println("Clients:", clients)
	}

	benchmark := bench.NewBenchmark(&conf.Request, conf.Params.RequestRatePerSec, conf.Params.Clients, conf.Params.Duration, conf.Params.BaseLatency)
	summary, err := benchmark.Run(conf.Params.OutputJSON, conf.Params.TightTicker)
	maybePanic(err)

	fmt.Println("timeEnd   =", time.Now().UTC().Add(5*time.Second).Round(time.Second))

	fmt.Println(summary)

	outfile := conf.Output
	if outfile == "" {
		outfile = "out/res.hgrm"
	}

	err = os.MkdirAll(path.Dir(outfile), os.ModeDir|os.ModePerm)
	maybePanic(err)

	err = summary.GenerateLatencyDistribution(bench.Logarithmic, outfile)
	maybePanic(err)
}
