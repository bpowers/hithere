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
	"fmt"
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
const (
	maxResult      = 1000000
	maxConcurrency = 1000000
	maxIdleConn    = 500
)

type Reporter interface {
	Start()
	Finish(r *Result)
}

type Requester interface {
	Do(ctx context.Context, c *http.Client, reporter Reporter) (nRequests int, err error)
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

	// RPS is the requests per second to target generating
	RPS int

	// N is the total number of requests to make.
	N int

	// H2 is an option to make HTTP/2 requests
	H2 bool

	// Timeout in seconds.
	Timeout int

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

type workReporter struct {
	results chan<- *Result
}

var _ Reporter = (*workReporter)(nil)

func (w *workReporter) Finish(r *Result) {
	w.results <- r
}

func (w *workReporter) Start() {
	// TODO
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
		b.results = make(chan *Result, maxResult)
		b.stopCh = make(chan struct{}, maxConcurrency)
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
	for i := 0; i < maxConcurrency; i++ {
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

func (b *Work) makeRequests(c *http.Client, r *workReporter) int {
	ctx := context.Background()

	n, err := b.Requester.Clone().Do(ctx, c, r)
	if err != nil {
		log.Printf("requester.Do: %s", err)
	}

	return n
}

func (b *Work) runWorker(client *http.Client, n int) int {
	count := 0
	reporter := &workReporter{b.results}

	// if n == 0, run forever
	i := -1
	if n > 0 {
		i = 0
	}
	for i < n {
		// Check if application is stopped. Do not send into a closed channel.
		select {
		case <-b.stopCh:
			return count
		default:
			count += b.makeRequests(client, reporter)
		}
		if n > 0 {
			i++
		}
	}

	return count
}

func (b *Work) runN(client *http.Client) {

	var wg sync.WaitGroup
	// Ignore the case where b.N % b.C != 0.
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go func() {
			b.runWorker(client, b.N)
			wg.Done()
		}()
	}
	wg.Wait()
}

func (b *Work) timeOne(client *http.Client) (int, time.Duration) {
	start := now()
	n := b.runWorker(client, 1)
	return n, now() - start
}

func (b *Work) runRPS(client *http.Client) {
	n, delta := b.timeOne(client)

	fmt.Printf("%d requests took %s\n", n, delta.String())
}

func (b *Work) runWorkers() {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		MaxIdleConnsPerHost: maxIdleConn,
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

	if b.N > 0 {
		b.runN(client)
	} else {
		b.runRPS(client)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
