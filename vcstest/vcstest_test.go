// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package vcstest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-widgets/vcs"
	"github.com/go-widgets/vcs/vcstest"
)

func TestRepo(t *testing.T) {
	r := vcstest.NewRepo("/work")
	if r.Root() != "/work" {
		t.Errorf("Root = %q", r.Root())
	}
	got, err := r.Status(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty Status = %v, %v", got, err)
	}
	in := map[string]vcs.State{"a.txt": vcs.Modified}
	r.Set(in)
	in["a.txt"] = vcs.Deleted // the caller's map is not shared
	got, _ = r.Status(context.Background())
	if got["a.txt"] != vcs.Modified {
		t.Errorf("Set must copy: %v", got)
	}
	got["a.txt"] = vcs.Added // nor is the reported one
	if again, _ := r.Status(context.Background()); again["a.txt"] != vcs.Modified {
		t.Error("Status must return a copy")
	}
	r.Set(nil)
	if got, _ = r.Status(context.Background()); got == nil || len(got) != 0 {
		t.Errorf("Set(nil) = %v, want an empty non-nil map", got)
	}

	boom := errors.New("boom")
	r.Fail(boom)
	if _, err = r.Status(context.Background()); err != boom {
		t.Errorf("Fail: err = %v", err)
	}
	r.Fail(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
	if r.Calls() != 6 {
		t.Errorf("Calls = %d, want 6", r.Calls())
	}
}

func TestDriver(t *testing.T) {
	r := vcstest.NewRepo("/work/proj/")
	d := vcstest.NewDriver(r)
	if !d.Recognises("/work/proj") || d.Recognises("/work") {
		t.Error("Recognises must match exactly the repository roots")
	}
	got, err := d.Open("/work/proj")
	if err != nil || got != vcs.Repository(r) {
		t.Errorf("Open = %v, %v", got, err)
	}
	if _, err = d.Open("/elsewhere"); !errors.Is(err, vcs.ErrNoRepository) {
		t.Errorf("Open(unknown) err = %v", err)
	}
}
