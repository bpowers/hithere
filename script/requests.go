// Copyright 2019 The hithere Authors. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, that can be found in the LICENSE file.

package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"go.starlark.net/starlark"

	"github.com/bpowers/hithere/script/starlarkjson"
)

var responseAttrs = []string{
	"status_code", // int
	"headers",     // CaseInsensitiveDict[str]
	// raw: Any
	"url",      // url: str
	"encoding", // str
	// history: List[Response]
	"reason", // str
	// cookies: RequestsCookieJar
	// elapsed: datetime.timedelta
	// request: PreparedRequest

	"ok", // def ok(self) -> bool: ...

	// def content(self) -> bytes: ...

	"text", // def text(self) -> str: ...
	"json", // def json(self, **kwargs) -> Any: ...

	"raise_for_status", // def raise_for_status(self) -> None: ...
}

type response struct {
	resp *http.Response
	body []byte
}

func newResponse(resp *http.Response) (*response, error) {
	// fully read the body once to match Python's behavior
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ioutil.ReadAll: %w", err)
	}

	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("Body.Close(): %w", err)
	}

	return &response{
		resp: resp,
		body: body,
	}, nil
}

func (r *response) Attr(name string) (starlark.Value, error) {
	switch name {
	case "status_code":
		return starlark.MakeInt(r.resp.StatusCode), nil
	case "url":
		return starlark.String(r.resp.Request.URL.String()), nil
	case "ok":
		code := r.resp.StatusCode
		if code < 400 {
			return starlark.True, nil
		} else {
			return starlark.False, nil
		}
	case "text":
		return starlark.String(string(r.body)), nil
	case "raise_for_status", "json":
		return &responseAttr{r, name}, nil
	}
	// returns (nil, nil) if attribute not present
	return nil, nil
}

func (r *response) String() string {
	return ""
}

func (r *response) Type() string {
	return "response"
}
func (r *response) Freeze() {}
func (r *response) Truth() starlark.Bool {
	return starlark.True
}
func (r *response) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", r.Type())
}

func (r *response) AttrNames() []string {
	// callers must not modify the result.
	return []string{}
}

type responseAttr struct {
	r    *response
	attr string
}

func (r *responseAttr) String() string {
	return r.Name()
}

func (r *responseAttr) Name() string {
	return fmt.Sprintf("response.%s", r.attr)
}

func (r *responseAttr) Type() string {
	return "response"
}
func (r *responseAttr) Freeze() {}
func (r *responseAttr) Truth() starlark.Bool {
	return starlark.True
}
func (r *responseAttr) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", r.Type())
}

func (r *responseAttr) json() (starlark.Value, error) {
	var x interface{}
	if err := json.Unmarshal(r.r.body, &x); err != nil {
		return nil, fmt.Errorf("response.json: %w", err)
	}
	var decode func(x interface{}) (starlark.Value, error)
	decode = func(x interface{}) (starlark.Value, error) {
		switch x := x.(type) {
		case nil:
			return starlark.None, nil
		case bool:
			return starlark.Bool(x), nil
		case int:
			return starlark.MakeInt(x), nil
		case float64:
			return starlark.Float(x), nil
		case string:
			return starlark.String(x), nil
		case map[string]interface{}: // object
			dict := new(starlark.Dict)
			for k, v := range x {
				vv, err := decode(v)
				if err != nil {
					return nil, fmt.Errorf("in object field .%s, %v", k, err)
				}
				dict.SetKey(starlark.String(k), vv) // can't fail
			}
			return dict, nil
		case []interface{}: // array
			tuple := make(starlark.Tuple, len(x))
			for i, v := range x {
				vv, err := decode(v)
				if err != nil {
					return nil, fmt.Errorf("at array index %d, %v", i, err)
				}
				tuple[i] = vv
			}
			return tuple, nil
		}
		panic(x) // unreachable
	}
	v, err := decode(x)
	if err != nil {
		return nil, fmt.Errorf("response.json: %w", err)
	}
	return v, nil
}

func (r *responseAttr) CallInternal(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	switch r.attr {
	case "raise_for_status":
		code := r.r.resp.StatusCode
		if code >= 400 {
			return nil, fmt.Errorf("HTTP %d from %s", code, r.r.resp.Request.URL)
		}
	case "json":
		return r.json()
	}
	return starlark.None, nil
}

var _ starlark.HasAttrs = (*response)(nil)
var _ starlark.Callable = (*responseAttr)(nil)

type requestsModule struct {
	Module
}

func RequestsModule() *requestsModule {
	r := &requestsModule{
		Module: Module{
			Name: "requests",
			Attrs: starlark.StringDict{
				"get":  starlark.None,
				"post": starlark.None,
			},
		},
	}

	r.Attrs["get"] = starlark.NewBuiltin("requests.get", r.fnRequestsGet)
	r.Attrs["post"] = starlark.NewBuiltin("requests.post", r.fnRequestsPost)

	return r
}

func (r *requestsModule) fnRequestsGet(t *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var c *http.Client
	var ok bool
	if c, ok = t.Local("requests_client").(*http.Client); !ok {
		return starlark.None, fmt.Errorf("requests can't be used at top level, only in function bodies")
	}
	if c == nil {
		return starlark.None, fmt.Errorf("expected non-nil requests_client")
	}

	var urlString starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &urlString); err != nil {
		return nil, err
	}

	var url starlark.String
	if url, ok = urlString.(starlark.String); !ok {
		return starlark.None, fmt.Errorf("expected url to be a string")
	}

	req, err := http.NewRequest("GET", url.GoString(), nil)
	if err != nil {
		return starlark.None, fmt.Errorf("http.NewRequest: %w", err)
	}

	goresp, err := c.Do(req)
	if err != nil {
		return starlark.None, fmt.Errorf("r.c.Do: %w", err)
	}

	return newResponse(goresp)
}

func (r *requestsModule) fnRequestsPost(t *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var c *http.Client
	var ok bool
	if c, ok = t.Local("requests_client").(*http.Client); !ok {
		return starlark.None, fmt.Errorf("requests can't be used at top level, only in function bodies")
	}
	if c == nil {
		return starlark.None, fmt.Errorf("expected non-nil requests_client")
	}

	var urlString, dataVal, headersVal starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &urlString, "data", &dataVal, "headers", &headersVal); err != nil {
		return nil, fmt.Errorf("UnpackArgs: %w", err)
	}

	var body io.Reader
	if data, ok := dataVal.(starlark.String); ok {
		body = bytes.NewReader([]byte(data))
	} else if data, ok := dataVal.(*starlark.Dict); ok {
		urlData := url.Values{}
		for _, kVal := range data.Keys() {
			var k string
			if kStr, ok := kVal.(starlark.String); ok {
				k = kStr.GoString()
			} else {
				k = kVal.String()
			}
			vVal, found, err := data.Get(kVal)
			if !found {
				return nil, fmt.Errorf("data.Get(%v): %w", k, err)
			}
			if v, ok := vVal.(starlark.String); ok {
				urlData.Set(k, v.GoString())
			} else {
				v, err := starlarkjson.Encode(t, fn, []starlark.Value{vVal}, nil)
				if err != nil {
					return nil, fmt.Errorf("starjson.Encode(%v): %w", v, err)
				}
				urlData.Set(k, v.(starlark.String).GoString())
			}
		}
		body = strings.NewReader(urlData.Encode())
	} else {
		return starlark.None, fmt.Errorf("expected a string or dict for data")
	}

	var url starlark.String
	if url, ok = urlString.(starlark.String); !ok {
		return starlark.None, fmt.Errorf("expected url to be a string")
	}

	req, err := http.NewRequest("POST", url.GoString(), body)
	if err != nil {
		return starlark.None, fmt.Errorf("http.NewRequest: %w", err)
	}

	if headers, ok := headersVal.(*starlark.Dict); ok {
		for _, kVal := range headers.Keys() {
			var k string
			if kStr, ok := kVal.(starlark.String); ok {
				k = kStr.GoString()
			} else {
				k = kVal.String()
			}
			vVal, found, err := headers.Get(kVal)
			if !found {
				return nil, fmt.Errorf("data.Get(%v): %w", kVal, err)
			}
			var v string
			if vStr, ok := vVal.(starlark.String); ok {
				v = vStr.GoString()
			} else {
				v = vVal.String()
			}
			req.Header.Set(k, v)
		}
	} else {
		return starlark.None, fmt.Errorf("expected a dict for headers")
	}

	goresp, err := c.Do(req)
	if err != nil {
		return starlark.None, fmt.Errorf("r.c.Do: %w", err)
	}

	return newResponse(goresp)
}
