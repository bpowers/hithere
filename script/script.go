// Copyright 2019 The hithere Authors. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, that can be found in the LICENSE file.

package script

import (
	"context"
	"fmt"
	"go.starlark.net/starlark"
	"net/http"

	_ "github.com/golang/protobuf/ptypes/wrappers"
	"github.com/stripe/skycfg"

	"github.com/bpowers/hithere/requester"
)

type Script struct {
	config skycfg.Config
}

func New(path string) (*Script, error) {
	s := &Script{}

	globals := make(starlark.StringDict)

	ctx := context.Background()
	config, err := skycfg.Load(ctx, path, skycfg.WithGlobals(globals))
	if err != nil {
		return nil, fmt.Errorf("skycfg.Load(%s): %w", path, err)
	}

	s.config = *config
	return s, nil
}

func (s *Script) Do(ctx context.Context, c *http.Client) (nRequests int, err error) {
	_, err = s.config.Main(ctx)
	if err != nil {
		return nRequests, fmt.Errorf("main: %w", err)
	}

	return
}

func (s *Script) Clone() requester.Requester {
	return s
}
