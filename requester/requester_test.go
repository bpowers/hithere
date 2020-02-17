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

package requester

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type testRequester struct {
	req  *http.Request
	body []byte
}

func (t *testRequester) Do(ctx context.Context, c *http.Client, _ Reporter) error {
	resp, err := c.Do(t.req)
	if err != nil {
		fmt.Printf("ah shit.\n")
		return fmt.Errorf("c.Do: %w", err)
	}

	io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()

	return nil
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request, body []byte) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}
	if len(body) > 0 {
		r2.Body = ioutil.NopCloser(bytes.NewReader(body))
	}
	return r2
}

func (t *testRequester) Clone() Requester {
	return &testRequester{
		req: cloneRequest(t.req, t.body),
	}
}

func TestN(t *testing.T) {
	var count int64
	handler := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, int64(1))
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	w := &Work{
		Requester: &testRequester{req, nil},
		N:         20,
	}
	w.Run()
	if count != 20 {
		t.Errorf("Expected to send 20 requests, found %v", count)
	}
}

func TestRequest(t *testing.T) {
	var uri, contentType, some, method, auth string
	handler := func(w http.ResponseWriter, r *http.Request) {
		uri = r.RequestURI
		method = r.Method
		contentType = r.Header.Get("Content-type")
		some = r.Header.Get("X-some")
		auth = r.Header.Get("Authorization")
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	header := make(http.Header)
	header.Add("Content-type", "text/html")
	header.Add("X-some", "value")
	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Header = header
	req.SetBasicAuth("username", "password")
	w := &Work{
		Requester: &testRequester{req, nil},
		N:         1,
	}
	w.Run()
	if uri != "/" {
		t.Errorf("Uri is expected to be /, %v is found", uri)
	}
	if contentType != "text/html" {
		t.Errorf("Content type is expected to be text/html, %v is found", contentType)
	}
	if some != "value" {
		t.Errorf("X-some header is expected to be value, %v is found", some)
	}
	if auth != "Basic dXNlcm5hbWU6cGFzc3dvcmQ=" {
		t.Errorf("Basic authorization is not properly set")
	}
	if method != "GET" {
		t.Errorf("expected GET method")
	}
}

func TestBody(t *testing.T) {
	var count int64
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		if string(body) == "Body" {
			atomic.AddInt64(&count, 1)
		} else {
			fmt.Printf("welp.\n")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL, bytes.NewBuffer([]byte("Body")))
	w := &Work{
		Requester: &testRequester{req, []byte("Body")},
		N:         10,
	}
	w.Run()
	if count != 10 {
		t.Errorf("Expected to work 10 times, found %v", count)
	}
}
