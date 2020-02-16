// Copyright 2020 The hithere Authors. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, that can be found in the LICENSE file.

package script

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.starlark.net/starlark"

	"github.com/bpowers/hithere/requester"
	"github.com/bpowers/hithere/script/starlarkjson"
)

type Script struct {
	config Config
}

// predeclaredModules is a helper that returns new predeclared modules.
// Returns proto module separately for (optional) extra initialization.
func predeclaredModules() (modules starlark.StringDict) {
	return starlark.StringDict{
		"json":     starlarkjson.Module,
		"requests": RequestsModule(),
	}
}

func print(t *starlark.Thread, msg string) {
	_, _ = fmt.Fprintf(os.Stderr, "[%v] %s\n", t.CallFrame(1).Pos, msg)
}

// A FileReader controls how load() calls resolve and read other modules.
type FileReader interface {
	// Resolve parses the "name" part of load("name", "symbol") to a path. This
	// is not required to correspond to a true path on the filesystem, but should
	// be "absolute" within the semantics of this FileReader.
	//
	// fromPath will be empty when loading the root module passed to Load().
	Resolve(ctx context.Context, name, fromPath string) (path string, err error)

	// ReadFile reads the content of the file at the given path, which was
	// returned from Resolve().
	ReadFile(ctx context.Context, path string) ([]byte, error)
}

type localFileReader struct {
	root string
}

// LocalFileReader returns a FileReader that resolves and loads files from
// within a given filesystem directory.
func LocalFileReader(root string) FileReader {
	if root == "" {
		panic("LocalFileReader: empty root path")
	}
	return &localFileReader{root}
}

func (r *localFileReader) Resolve(ctx context.Context, name, fromPath string) (string, error) {
	if fromPath == "" {
		return name, nil
	}
	if filepath.Separator != '/' && strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("load(%q): invalid character in module name", name)
	}
	resolved := filepath.Join(r.root, filepath.FromSlash(path.Clean("/"+name)))
	return resolved, nil
}

func (r *localFileReader) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

// A Config is a hithere script that has been fully loaded and is ready
// for execution.
type Config struct {
	filename string
	globals  starlark.StringDict
	locals   starlark.StringDict
}

type loadOptions struct {
	globals    starlark.StringDict
	fileReader FileReader
}

var (
	ifNameMainRe     = regexp.MustCompile("(?s)\nif\\s+__name__\\s+==.*$")
	importRequestsRe = regexp.MustCompile("import\\s+requests")
	shebangRe        = regexp.MustCompile("^#!.*")
)

// from skycfg
func loadImpl(ctx context.Context, opts *loadOptions, filename string) (starlark.StringDict, error) {
	reader := opts.fileReader

	type cacheEntry struct {
		globals starlark.StringDict
		err     error
	}
	cache := make(map[string]*cacheEntry)

	var load func(thread *starlark.Thread, moduleName string) (starlark.StringDict, error)
	load = func(thread *starlark.Thread, moduleName string) (starlark.StringDict, error) {
		var fromPath string
		if thread.CallStackDepth() > 0 {
			fromPath = thread.CallFrame(0).Pos.Filename()
		}
		modulePath, err := reader.Resolve(ctx, moduleName, fromPath)
		if err != nil {
			return nil, err
		}

		e, ok := cache[modulePath]
		if e != nil {
			return e.globals, e.err
		}
		if ok {
			return nil, fmt.Errorf("cycle in load graph")
		}
		moduleSource, err := reader.ReadFile(ctx, modulePath)
		if err != nil {
			cache[modulePath] = &cacheEntry{nil, err}
			return nil, err
		}

		moduleSource = shebangRe.ReplaceAll(moduleSource, []byte(""))
		moduleSource = importRequestsRe.ReplaceAll(moduleSource, []byte(""))
		moduleSource = ifNameMainRe.ReplaceAll(moduleSource, []byte("\n"))

		cache[modulePath] = nil
		globals, err := starlark.ExecFile(thread, modulePath, moduleSource, opts.globals)
		cache[modulePath] = &cacheEntry{globals, err}

		return globals, err
	}
	locals, err := load(&starlark.Thread{
		Print: print,
		Load:  load,
	}, filename)
	return locals, err
}

func New(filename string) (*Script, error) {
	s := &Script{}

	ctx := context.Background()

	modules := predeclaredModules()
	parsedOpts := &loadOptions{
		globals:    modules,
		fileReader: LocalFileReader(filepath.Dir(filename)),
	}
	scriptLocals, err := loadImpl(ctx, parsedOpts, filename)
	if err != nil {
		return nil, err
	}

	s.config = Config{
		filename: filename,
		globals:  parsedOpts.globals,
		locals:   scriptLocals,
	}
	return s, nil
}

func (s *Script) Do(ctx context.Context, c *http.Client) (nRequests int, err error) {
	vars := &starlark.Dict{}

	mainVal, ok := s.config.locals["main"]
	if !ok {
		return 0, fmt.Errorf("no `main' function found in %q", s.config.filename)
	}
	main, ok := mainVal.(starlark.Callable)
	if !ok {
		return 0, fmt.Errorf("`main' must be a function (got a %s)", mainVal.Type())
	}

	thread := &starlark.Thread{
		Print: print,
	}
	thread.SetLocal("context", ctx)
	thread.SetLocal("requests_client", c)
	mainCtx := &Module{
		Name: "hithere_ctx",
		Attrs: starlark.StringDict(map[string]starlark.Value{
			"vars": vars,
		}),
	}
	args := starlark.Tuple([]starlark.Value{mainCtx})
	_, err = starlark.Call(thread, main, args, nil)
	if err != nil {
		return 0, err
	}

	return 1, nil
}

func (s *Script) Clone() requester.Requester {
	return s
}
