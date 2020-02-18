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
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paulbellamy/ratecounter"
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
	UserAgent() string
}

type Requester interface {
	Do(ctx context.Context, c *http.Client, reporter Reporter) (err error)
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

	UserAgent string

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

	initOnce     sync.Once
	results      chan *Result
	stopCh       chan struct{}
	workerStopCh chan struct{}
	start        time.Duration

	report *report

	workerCount int32

	counter1s *ratecounter.RateCounter
	counter5s *ratecounter.RateCounter
}

type workReporter struct {
	counter1s *ratecounter.RateCounter
	counter5s *ratecounter.RateCounter
	results   chan<- *Result
	count     uint32
	userAgent string
}

var _ Reporter = (*workReporter)(nil)

func (w *workReporter) Finish(r *Result) {
	w.results <- r
}

func (w *workReporter) Start() {
	atomic.AddUint32(&w.count, 1)
	w.counter1s.Incr(1)
	w.counter5s.Incr(1)
}

func (w *workReporter) Count() int {
	return int(atomic.LoadUint32(&w.count))
}

func (w *workReporter) UserAgent() string {
	return w.userAgent
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
		b.workerStopCh = make(chan struct{}, maxConcurrency)
		b.counter1s = ratecounter.NewRateCounter(2 * time.Second)
		b.counter5s = ratecounter.NewRateCounter(5 * time.Second)
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

func (b *Work) makeRequests(c *http.Client, r *workReporter) {
	ctx := context.Background()

	err := b.Requester.Clone().Do(ctx, c, r)
	if err != nil {
		log.Printf("requester.Do: %s", err)
	}
}

func (b *Work) incWorkerCount() {
	atomic.AddInt32(&b.workerCount, 1)
}

func (b *Work) decWorkerCount() {
	atomic.AddInt32(&b.workerCount, -1)
}

func (b *Work) getWorkerCount() int {
	return int(atomic.LoadInt32(&b.workerCount))
}

func (b *Work) runWorker(client *http.Client, n int) int {
	reporter := &workReporter{
		counter1s: b.counter1s,
		counter5s: b.counter5s,
		results:   b.results,
		count:     0,
		userAgent: b.UserAgent,
	}

	// if n == 0, run forever
	i := -1
	if n > 0 {
		i = 0
	}
	for i < n {
		// Check if application is stopped. Do not send into a closed channel.
		select {
		case <-b.stopCh:
			return reporter.Count()
		case <-b.workerStopCh:
			return reporter.Count()
		default:
			b.makeRequests(client, reporter)
		}
		if n > 0 {
			i++
		}
	}

	return reporter.Count()
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
	reporter := &workReporter{
		counter1s: ratecounter.NewRateCounter(1 * time.Second),
		counter5s: ratecounter.NewRateCounter(5 * time.Second),
		results:   make(chan *Result, maxResult),
		count:     0,
		userAgent: b.UserAgent,
	}
	defer func() {
		close(reporter.results)
	}()

	start := now()
	b.makeRequests(client, reporter)
	duration := now() - start

	return reporter.Count(), duration
}

func (b *Work) runRPSWorker(origDeltaMs float64, wg *sync.WaitGroup, client *http.Client) {
	wg.Add(1)
	go func() {
		b.incWorkerCount()
		defer b.decWorkerCount()

		// ensure we don't end up with all these workers in lock-step
		maxSleep := math.Ceil(origDeltaMs * 1.2)
		randSleep := rand.Float64() * maxSleep
		sleep := time.Duration(int64(math.Ceil(randSleep))) * time.Millisecond
		time.Sleep(sleep)

		b.runWorker(client, 0)

		wg.Done()
	}()
}

func (b *Work) runRPS(client *http.Client) {
	n, origDelta := b.timeOne(client)
	origDeltaMs := float64(origDelta.Milliseconds())
	rpsMeasured := float64(n) / origDelta.Seconds()
	rpsTarget := float64(b.RPS)

	b.start = now()

	// target rps / n workers = measured rps / 1 worker

	nWorkers := max(int(math.Ceil(rpsTarget/rpsMeasured)), 1)
	fmt.Printf("%d workers for %f RPS (%d / %f sec)\n", nWorkers, rpsTarget, n, origDelta.Seconds())

	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		b.runRPSWorker(origDeltaMs, &wg, client)
	}

	b.consoleReport(origDeltaMs, &wg, client)
}

func (b *Work) consoleReport(origDeltaMs float64, wg *sync.WaitGroup, client *http.Client) {
	const dt = 5
	const Kp = 5
	const Ki = 3
	const Kd = 3
	time.Sleep(5 * time.Second)

	ticker := time.NewTicker(dt * time.Second)
	defer func() {
		ticker.Stop()
	}()

	defer func() {
		wg.Wait()
	}()

	prevError := float64(0)
	integral := float64(0)

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			rpsA := float64(b.counter1s.Rate()) / 2
			rpsB := float64(b.counter5s.Rate()) / 5
			rpsMeasured := (rpsA + rpsB) / 2
			rpsTarget := float64(b.RPS)

			// target rps / target workers = measured rps / m workers
			// m workers * target rps / measured rps = target workers

			workers := b.getWorkerCount()
			workerGoalFloat := float64(workers) * rpsTarget / rpsMeasured
			workerGoal := max(int(math.Ceil(workerGoalFloat)), 1)
			fmt.Printf("\tgoal %d (%.1f)\n", workerGoal, workerGoalFloat)

			error := float64(workerGoal - workers)
			integral = integral + error*dt
			derivative := (error - prevError) / dt
			output := Kp*error + Ki*integral + Kd*derivative
			prevError = error

			newWorkers := float64(workers) * (1 + output/100)
			workerDiff := int(math.Round(newWorkers)) - workers

			fmt.Printf("current: %.1f rps (%d workers) (error: %.1f out: %.1f, newWorkers: %.1f)\n", rpsMeasured, b.getWorkerCount(), error, output, newWorkers)

			// avoid flip flopping around by ignoring 1 worker diffs
			if workerDiff > 1 {
				//fmt.Printf("spawning %d new workers\n", workerDiff)
				//for i := 0; i < workerDiff; i++ {
				//	b.runRPSWorker(origDeltaMs, wg, client)
				//}
			} else if workerDiff < -1 {
				//fmt.Printf("killing %d workers\n", -workerDiff)
				//for i := 0; i < -workerDiff; i++ {
				//	b.workerStopCh <- struct{}{}
				//}
			}
		}
	}
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
		if err := http2.ConfigureTransport(tr); err != nil {
			log.Fatalf("http2.ConfigureTransport: %s", err)
		}
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
