// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

// Package vcstest is a fake version-control driver, so code that consumes a
// [vcs.Repository] (a file explorer, say) can be tested without any real
// version-control system.
package vcstest

import (
	"context"
	"maps"
	"path/filepath"
	"sync"

	"github.com/go-widgets/vcs"
)

// Repo is an in-memory [vcs.Repository] whose status the test sets.
type Repo struct {
	root string

	mu     sync.Mutex
	states map[string]vcs.State
	err    error
	calls  int
}

// NewRepo returns a fake repository rooted at root.
func NewRepo(root string) *Repo {
	return &Repo{root: root, states: map[string]vcs.State{}}
}

// Root implements [vcs.Repository].
func (r *Repo) Root() string { return r.root }

// Set replaces the status Status will report (the map is copied).
func (r *Repo) Set(states map[string]vcs.State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = maps.Clone(states)
	if r.states == nil {
		r.states = map[string]vcs.State{}
	}
}

// Fail makes Status return err (nil restores success).
func (r *Repo) Fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// Calls is how many times Status has been called.
func (r *Repo) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// Status implements [vcs.Repository]: a copy of what Set gave, the error Fail
// gave, or ctx's error when it is already done.
func (r *Repo) Status(ctx context.Context) (map[string]vcs.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	return maps.Clone(r.states), nil
}

// Driver is a fake [vcs.Driver] that recognises exactly the directories in Repos.
type Driver struct {
	// Repos maps a cleaned absolute directory to the repository it holds.
	Repos map[string]*Repo
}

// NewDriver returns a driver that recognises the given repositories by Root.
func NewDriver(repos ...*Repo) *Driver {
	d := &Driver{Repos: map[string]*Repo{}}
	for _, r := range repos {
		d.Repos[filepath.Clean(r.root)] = r
	}
	return d
}

// Recognises implements [vcs.Driver].
func (d *Driver) Recognises(dir string) bool {
	_, ok := d.Repos[filepath.Clean(dir)]
	return ok
}

// Open implements [vcs.Driver].
func (d *Driver) Open(dir string) (vcs.Repository, error) {
	r, ok := d.Repos[filepath.Clean(dir)]
	if !ok {
		return nil, vcs.ErrNoRepository
	}
	return r, nil
}
