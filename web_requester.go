package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/net/http2"

	"time"

	"labench/bench"
)

var (
	httpClient    *http.Client
	defaultDialer *net.Dialer
	noLinger      bool
)

func noLingerDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	con, err := defaultDialer.DialContext(ctx, network, addr)
	if err == nil && con != nil && noLinger {
		maybePanic(con.(*net.TCPConn).SetLinger(0))
	}
	return con, err
}

func initHTTPClient(reuseConnections bool, requestTimeout time.Duration, dontLinger bool) {
	defaultDialer = &net.Dialer{
		Timeout: requestTimeout,
		// Disable TCP keepalives as we are sending data very actively anyway.
		// Should not be confused with HTTP keep alive.
		KeepAlive: 0,
	}

	httpClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           noLingerDialer,
			DisableKeepAlives:     !reuseConnections,
			MaxIdleConns:          0,
			MaxIdleConnsPerHost:   0,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: requestTimeout,
			TLSHandshakeTimeout:   requestTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: requestTimeout}

	noLinger = dontLinger
}

func initHTTP2Client(requestTimeout time.Duration, dontLinger bool) {
	defaultDialer = &net.Dialer{
		Timeout: requestTimeout,
		// Disable TCP keepalives as we are sending data very actively anyway.
		// Should not be confused with HTTP keep alive.
		KeepAlive: 0,
	}

	httpClient = &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				con, err := defaultDialer.Dial(network, addr)
				if err == nil && con != nil && noLinger {
					maybePanic(con.(*net.TCPConn).SetLinger(0))
				}
				return con, err
			},
		},
		Timeout: requestTimeout}

	noLinger = dontLinger
}

// WebRequesterFactory implements RequesterFactory by creating a Requester
// which makes GET requests to the provided URL.
type WebRequesterFactory struct {
	URL                    string            `yaml:"URL"`
	URLs                   []string          `yaml:"URLs"`
	Hosts                  []string          `yaml:"Hosts"`
	Headers                map[string]string `yaml:"Headers"`
	Body                   string            `yaml:"Body"`
	BodyFile               string            `yaml:"BodyFile"`
	ExpectedHTTPStatusCode int               `yaml:"ExpectedHTTPStatusCode"`
	HTTPMethod             string            `yaml:"HTTPMethod"`

	expandedHeaders map[string][]string
}

// GetRequester returns a new Requester, called for each Benchmark connection.
func (w *WebRequesterFactory) GetRequester(uint64) bench.Requester {
	// if len(w.expandedHeaders) != len(w.Headers) {
	if w.expandedHeaders == nil {
		expandedHeaders := make(map[string][]string)
		for key, val := range w.Headers {
			expandedHeaders[key] = []string{os.ExpandEnv(val)}
		}
		w.expandedHeaders = expandedHeaders
	}

	// if BodyFile is specified Body is ignored
	if w.BodyFile != "" {
		content, err := ioutil.ReadFile(w.BodyFile)
		maybePanic(err)
		w.Body = string(content)
	}

	return &webRequester{w.URL, w.URLs, w.Hosts, w.expandedHeaders, w.Body, w.ExpectedHTTPStatusCode, w.HTTPMethod}
}

// webRequester implements Requester by making a GET request to the provided
// URL.
type webRequester struct {
	url                string
	urls               []string
	hosts              []string
	headers            map[string][]string
	body               string
	expectedReturnCode int
	httpMethod         string
}

var nextHostOrURL int32 = -1

// Setup prepares the Requester for benchmarking.
func (w *webRequester) Setup() error { return nil }

// Request performs a synchronous request to the system under test.
func (w *webRequester) Request() error {
	var reqURL string
	if w.urls != nil {
		h := atomic.AddInt32(&nextHostOrURL, 1)
		reqURL = w.urls[h%int32(len(w.urls))]
	} else if w.hosts != nil {
		parsedURL, err := url.Parse(w.url)
		if err != nil {
			return err
		}
		h := atomic.AddInt32(&nextHostOrURL, 1)
		parsedURL.Host = w.hosts[h%int32(len(w.hosts))]
		reqURL = parsedURL.String()
	} else {
		reqURL = w.url
	}

	req, err := http.NewRequest(w.httpMethod, reqURL, strings.NewReader(w.body))
	if err != nil {
		return err
	}

	req.Header = w.headers

	// from https://golang.org/src/net/http/request.go?#L124
	// For client requests, the URL's Host specifies the server to
	// connect to, while the Request's Host field optionally
	// specifies the Host header value to send in the HTTP
	// request.

	//case insensitive
	if host, ok := w.headers["host"]; ok {
		if len(host) != 1 {
			return errors.New("multiple host headers are not allowed")
		}
		req.Host = host[0]
	} else if host, ok = w.headers["Host"]; ok {
		if len(host) != 1 {
			return errors.New("multiple host headers are not allowed")
		}
		req.Host = host[0]
	}

	resp, err := httpClient.Do(req)

	/* to look at the response body
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	s := buf.String()
	_ = s
	*/

	// #nosec
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	if err != nil {
		return err
	}

	if resp == nil {
		return errors.New("Nil response")
	}

	if resp.StatusCode != w.expectedReturnCode {
		return fmt.Errorf("Expected %v got %v", w.expectedReturnCode, resp.StatusCode)
	}

	return nil
}

// Teardown is called upon benchmark completion.
func (w *webRequester) Teardown() error { return nil }
