// Copyright 2020 The hithere Authors. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, that can be found in the LICENSE file.

package script

import (
	"testing"

	"go.starlark.net/starlark"

	"github.com/bpowers/hithere/script/starlarkjson"
)

func TestNestedSerialize(t *testing.T) {
	cases := []struct {
		src      string
		expected string
	}{
		{
			src:      `{"a": "b"}`,
			expected: `a=b`,
		},
		{
			src:      `{"card": {"number": "4242424242424242"}}`,
			expected: `card[number]=4242424242424242`,
		},
	}

	for _, test := range cases {
		thread := &starlark.Thread{}
		builtin := starlarkjson.Module.Members["decode"].(*starlark.Builtin)
		var args starlark.Tuple = []starlark.Value{starlark.String(test.src)}
		val, err := starlarkjson.Decode(thread, builtin, args, nil)
		if err != nil {
			t.Fatalf("starlarkjson.Decode: %s", err)
		}
		body, err := urlencodeBody(val)
		if err != nil {
			t.Fatalf("urlencodeBody: %s", err)
		}
		if test.expected != body {
			t.Fatalf("expected equal:\n%s\n%s\n", test.expected, string(body))
		}
	}
}
