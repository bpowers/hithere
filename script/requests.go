// Copyright 2019 The hithere Authors. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, that can be found in the LICENSE file.

package script

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"go.starlark.net/starlark"
)

type requestsModule struct {
	Module
	c *http.Client
}

func RequestsModule(c *http.Client) starlark.Value {
	r := &requestsModule{
		Module: Module{
			Name: "requests",
			Attrs: starlark.StringDict{
				"get": starlark.None,
			},
		},
		c: c,
	}

	r.Attrs["get"] = starlark.NewBuiltin("requests.get", r.fnRequestsGet)

	return r
}

func (r *requestsModule) fnRequestsGet(t *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var urlString starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &urlString); err != nil {
		return nil, err
	}

	var ok bool
	var url starlark.String
	if url, ok = urlString.(starlark.String); !ok {
		return starlark.None, fmt.Errorf("expected url to be a string")
	}

	req, err := http.NewRequest("GET", url.GoString(), nil)
	if err != nil {
		return starlark.None, fmt.Errorf("http.NewRequest: %w", err)
	}

	resp, err := r.c.Do(req)
	io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()

	return starlark.String(fmt.Sprintf("code: %d", resp.StatusCode)), nil
}
