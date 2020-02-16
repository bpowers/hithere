// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package requester provides commands to run load tests and display results.
package requester

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Max size of the buffer of result channel.
const maxResult = 1000000
const maxIdleConn = 500

type Requester interface {
	Do(ctx context.Context, c *http.Client, reporter chan<- *Result) (nRequests int, err error)
	Clone() Requester
}

type Result struct {
	Err           error
	StatusCode    int
	Offset        time.Duration
	Duration      time.Duration
	ConnDuration  time.Duration // connection setup(DNS lookup + Dial up) duration
	DnsDuration   time.Duration // dns lookup duration
	ReqDuration   time.Duration // request "write" duration
	ResDuration   time.Duration // response "read" duration
	DelayDuration time.Duration // delay between response and request
	ContentLength int64
}

type Work struct {
	// Request is the request to be made.
	Requester Requester

	// N is the total number of requests to make.
	N int

	// C is the concurrency level, the number of concurrent workers to run.
	C int

	// H2 is an option to make HTTP/2 requests
	H2 bool

	// Timeout in seconds.
	Timeout int

	// Qps is the rate limit in queries per second.
	QPS float64

	// DisableCompression is an option to disable compression in response
	DisableCompression bool

	// DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	DisableKeepAlives bool

	// Output represents the output type. If "csv" is provided, the
	// output will be dumped as a csv stream.
	Output string

	// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
	// Optional.
	ProxyAddr *url.URL

	// Writer is where results will be written. If nil, results are written to stdout.
	Writer io.Writer

	initOnce sync.Once
	results  chan *Result
	stopCh   chan struct{}
	start    time.Duration

	report *report
}

func (b *Work) writer() io.Writer {
	if b.Writer == nil {
		return os.Stdout
	}
	return b.Writer
}

// Init initializes internal data-structures
func (b *Work) Init() {
	b.initOnce.Do(func() {
		b.results = make(chan *Result, min(b.C*1000, maxResult))
		b.stopCh = make(chan struct{}, b.C)
	})
}

// Run makes all the requests, prints the summary. It blocks until
// all work is done.
func (b *Work) Run() {
	b.Init()
	b.start = now()
	b.report = newReport(b.writer(), b.results, b.Output, b.N)
	// Run the reporter first, it polls the result channel until it is closed.
	go func() {
		runReporter(b.report)
	}()
	b.runWorkers()
	b.Finish()
}

func (b *Work) Stop() {
	// Send stop signal so that workers can stop gracefully.
	for i := 0; i < b.C; i++ {
		b.stopCh <- struct{}{}
	}
}

func (b *Work) Finish() {
	close(b.results)
	total := now() - b.start
	// Wait until the reporter is done.
	<-b.report.done
	b.report.finalize(total)
}

func (b *Work) makeRequest(c *http.Client) {
	ctx := context.Background()

	_, err := b.Requester.Clone().Do(ctx, c, b.results)
	if err != nil {
		log.Printf("requester.Do: %s", err)
	}
}

func (b *Work) runWorker(client *http.Client, n int) {
	var throttle <-chan time.Time
	if b.QPS > 0 {
		throttle = time.Tick(time.Duration(1e6/(b.QPS)) * time.Microsecond)
	}

	for i := 0; i < n; i++ {
		// Check if application is stopped. Do not send into a closed channel.
		select {
		case <-b.stopCh:
			return
		default:
			if b.QPS > 0 {
				<-throttle
			}
			b.makeRequest(client)
		}
	}
}

func (b *Work) runWorkers() {
	var wg sync.WaitGroup
	wg.Add(b.C)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		MaxIdleConnsPerHost: min(b.C, maxIdleConn),
		DisableCompression:  b.DisableCompression,
		DisableKeepAlives:   b.DisableKeepAlives,
		Proxy:               http.ProxyURL(b.ProxyAddr),
	}
	if b.H2 {
		http2.ConfigureTransport(tr)
	} else {
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{Transport: tr, Timeout: time.Duration(b.Timeout) * time.Second}

	// Ignore the case where b.N % b.C != 0.
	for i := 0; i < b.C; i++ {
		go func() {
			b.runWorker(client, b.N/b.C)
			wg.Done()
		}()
	}
	wg.Wait()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
