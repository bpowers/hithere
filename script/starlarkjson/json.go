// Copyright 2019 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package starlarkjson defines utilities for converting Starlark values
// to/from JSON strings. See www.json.org.
package starlarkjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Module is a Starlark module of JSON-related functions.
//
//   json = module(
//      encode,
//      decode,
//      indent,
//   )
//
// def encode(x):
//
// The encode function accepts one required positional argument,
// which it converts to JSON by cases:
// - Starlark None, bool, int, float, and string values are
//   encoded as their corresponding JSON atoms.
//   JSON has only one number data type.
//   It is an error to encode a non-finite floating-point value.
// - a Starlark IterableMapping (e.g. dict) is encoded as a JSON object.
//   It is an error if any key is not a string.
// - any other Starlark Iterable (e.g. list, tuple) is encoded as a JSON array.
// - a Starlark HasAttrs (e.g. struct) is encoded as an JSON object.
// If a user-defined type matches multiple interfaces (e.g. Iterable and
// HasFields), the earliest case described above wins.
// An application-defined Starlark value type that implements the
// standard json.Marshal Go interface defines its own JSON encoding.
// Encoding any other value yields an error.
//
// def decode(x):
//
// The decode function accepts one positional parameter, a JSON string.
// It returns the Starlark value that the string denotes.
// - Numbers may be parsed as as int or float, depending on magnitude.
// - JSON objects are parsed as Starlark dicts.
// - JSON arrays are parsed as Starlark tuples.
// Decoding fails if x is not a valid JSON string.
//
// def indent(str, *, prefix="", indent="\t"):
//
// The indent function pretty-prints a valid JSON encoding,
// and returns a string containing the indented form.
// It accepts one required positional parameter, the JSON string,
// and two optional keyword-only string parameters, prefix and indent,
// that specify a prefix of each new line, and the unit of indentation.
//
var Module = &starlarkstruct.Module{
	Name: "json",
	Members: starlark.StringDict{
		"encode": starlark.NewBuiltin("json.encode", Encode),
		"decode": starlark.NewBuiltin("json.decode", Decode),
		"indent": starlark.NewBuiltin("json.indent", indent),
	},
}

func Encode(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var x starlark.Value
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &x); err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)

	var quoteSpace [128]byte
	quote := func(s string) {
		if goQuoteIsSafe(s) {
			buf.Write(strconv.AppendQuote(quoteSpace[:0], s))
		} else {
			// vanishingly rare for text strings
			data, _ := json.Marshal(s)
			buf.Write(data)
		}
	}

	var emit func(x starlark.Value) error
	emit = func(x starlark.Value) error {
		switch x := x.(type) {
		case json.Marshaler:
			// Application-defined starlark.Value types
			// may define their own JSON encoding.
			data, err := x.MarshalJSON()
			if err != nil {
				return err
			}
			buf.Write(data)

		case starlark.NoneType:
			buf.WriteString("null")

		case starlark.Bool:
			fmt.Fprintf(buf, "%t", x)

		case starlark.Int:
			// JSON imposes no limit on numbers,
			// but the standard Go decoder may switch to float.
			fmt.Fprint(buf, x)

		case starlark.Float:
			if !isFinite(float64(x)) {
				return fmt.Errorf("cannot encode non-finite float %v", x)
			}
			fmt.Fprintf(buf, "%g", x)

		case starlark.String:
			quote(string(x))

		case starlark.IterableMapping:
			// e.g. dict (must have string keys)
			buf.WriteByte('{')
			iter := x.Iterate()
			defer iter.Done()
			var k starlark.Value
			for i := 0; iter.Next(&k); i++ {
				if i > 0 {
					buf.WriteByte(',')
				}
				s, ok := starlark.AsString(k)
				if !ok {
					return fmt.Errorf("%s has %s key, want string", x.Type(), k.Type())
				}
				v, found, err := x.Get(k)
				if err != nil || !found {
					log.Fatalf("internal error: mapping %s has %s among keys but value lookup fails", x.Type(), k)
				}

				quote(s)
				buf.WriteByte(':')
				if err := emit(v); err != nil {
					return fmt.Errorf("in %s key %s: %v", x.Type(), k, err)
				}
			}
			buf.WriteByte('}')

		case starlark.Iterable:
			// e.g. tuple, list
			buf.WriteByte('[')
			iter := x.Iterate()
			defer iter.Done()
			var elem starlark.Value
			for i := 0; iter.Next(&elem); i++ {
				if i > 0 {
					buf.WriteByte(',')
				}
				if err := emit(elem); err != nil {
					return fmt.Errorf("at %s index %d: %v", x.Type(), i, err)
				}
			}
			buf.WriteByte(']')

		case starlark.HasAttrs:
			// e.g. struct
			buf.WriteByte('{')
			var names []string
			names = append(names, x.AttrNames()...)
			sort.Strings(names)
			for i, name := range names {
				v, err := x.Attr(name)
				if err != nil || v == nil {
					log.Fatalf("internal error: dir(%s) includes %q but value has no .%s field", x.Type(), name, name)
				}
				if i > 0 {
					buf.WriteByte(',')
				}
				quote(name)
				buf.WriteByte(':')
				if err := emit(v); err != nil {
					return fmt.Errorf("in field .%s: %v", name, err)
				}
			}
			buf.WriteByte('}')

		default:
			return fmt.Errorf("cannot encode %s as JSON", x.Type())
		}
		return nil
	}

	if err := emit(x); err != nil {
		return nil, fmt.Errorf("%s: %v", b.Name(), err)
	}
	return starlark.String(buf.String()), nil
}

func goQuoteIsSafe(s string) bool {
	for _, r := range s {
		// JSON doesn't like Go's \xHH escapes for ASCII control codes,
		// nor its \UHHHHHHHH escapes for runes >16 bits.
		if r < 0x20 || r >= 0x10000 {
			return false
		}
	}
	return true
}

func indent(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	prefix, indent := "", "\t" // keyword-only
	if err := starlark.UnpackArgs(b.Name(), nil, kwargs,
		"prefix?", &prefix,
		"indent?", &indent,
	); err != nil {
		return nil, err
	}
	var str string // positional-only
	if err := starlark.UnpackPositionalArgs(b.Name(), args, nil, 1, &str); err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	if err := json.Indent(buf, []byte(str), prefix, indent); err != nil {
		return nil, fmt.Errorf("%s: %v", b.Name(), err)
	}
	return starlark.String(buf.String()), nil
}

func Decode(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var str string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &str); err != nil {
		return nil, err
	}

	// TODO(adonovan): design a mechanism whereby the caller can
	// control the types instantiated by the decoder (e.g. list
	// instead of tuple, or struct instead of dict; or any type that
	// satisfies json.Unmarshaller).

	// This implementation is just a sketch.
	// TODO(adonovan) reimplement it independent of the Go decoder.
	// For example, we could decode large integers to bigint, not float.
	var x interface{}
	if err := json.Unmarshal([]byte(str), &x); err != nil {
		return nil, fmt.Errorf("%s: %v", b.Name(), err)
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
		return nil, fmt.Errorf("%s: %v", b.Name(), err)
	}
	return v, nil
}

// isFinite reports whether f represents a finite rational value.
// It is equivalent to !math.IsNan(f) && !math.IsInf(f, 0).
func isFinite(f float64) bool {
	return math.Abs(f) <= math.MaxFloat64
}
