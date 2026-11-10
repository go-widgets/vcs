// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package vcs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// stub is a Driver that recognises dirs holding a marker file, and can fail Open.
type stub struct {
	marker  string
	openErr error
	opened  []string
	asked   []string
}

func (s *stub) Recognises(dir string) bool {
	s.asked = append(s.asked, dir)
	_, err := os.Stat(filepath.Join(dir, s.marker))
	return err == nil
}

func (s *stub) Open(dir string) (Repository, error) {
	s.opened = append(s.opened, dir)
	if s.openErr != nil {
		return nil, s.openErr
	}
	return rootRepo(dir), nil
}

type rootRepo string

func (r rootRepo) Root() string { return string(r) }
func (r rootRepo) Status(context.Context) (map[string]State, error) {
	return nil, nil
}

func register(t *testing.T, name string, d Driver) {
	t.Helper()
	Register(name, d)
	t.Cleanup(func() { Unregister(name) })
}

func TestStateString(t *testing.T) {
	want := map[State]string{
		Unchanged: "unchanged", Modified: "modified", Added: "added", Deleted: "deleted",
		Renamed: "renamed", Untracked: "untracked", Ignored: "ignored", Conflicted: "conflicted",
		State(-1): "State(-1)", State(99): "State(99)",
	}
	for s, w := range want {
		if got := s.String(); got != w {
			t.Errorf("State(%d) = %q, want %q", int(s), got, w)
		}
	}
	if Unchanged != 0 {
		t.Error("the zero State must be Unchanged")
	}
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	f()
}

func TestRegister(t *testing.T) {
	mustPanic(t, "nil driver", func() { Register("x", nil) })
	mustPanic(t, "empty name", func() { Register("", &stub{}) })
	register(t, "b", &stub{marker: "b"})
	register(t, "a", &stub{marker: "a"})
	mustPanic(t, "duplicate", func() { Register("a", &stub{}) })
	if got := Drivers(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Drivers = %v, want [a b]", got)
	}
	Unregister("never-registered") // ignored
}

func TestOpen(t *testing.T) {
	s := &stub{marker: "m"}
	register(t, "s", s)
	dir := t.TempDir()
	r, err := Open("s", dir)
	if err != nil || r.Root() != dir {
		t.Fatalf("Open = %v, %v", r, err)
	}
	if _, err := Open("nope", dir); err == nil {
		t.Error("an unknown driver must be an error")
	}
	absFunc = func(string) (string, error) { return "", errors.New("no cwd") }
	defer func() { absFunc = filepath.Abs }()
	if _, err := Open("s", "rel"); err == nil {
		t.Error("an Abs failure must be returned")
	}
}

func TestDetect(t *testing.T) {
	top := t.TempDir()
	sub := filepath.Join(top, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "f.txt")
	touch := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch(file)

	if _, err := Detect(sub); !errors.Is(err, ErrNoRepository) {
		t.Fatalf("with no driver, err = %v, want ErrNoRepository", err)
	}

	g := &stub{marker: ".g"}
	h := &stub{marker: ".h"}
	register(t, "g", g)
	register(t, "h", h)
	touch(filepath.Join(top, ".g"))

	r, err := Detect(sub) // walks up two levels
	if err != nil || r.Root() != top {
		t.Fatalf("Detect(dir) = %v, %v; want root %s", r, err, top)
	}
	g.asked = nil
	r, err = Detect(file) // a file starts at its directory
	if err != nil || r.Root() != top {
		t.Fatalf("Detect(file) = %v, %v; want root %s", r, err, top)
	}
	if g.asked[0] != sub {
		t.Errorf("first directory asked = %q, want %q (not the file itself)", g.asked[0], sub)
	}

	// The nearest directory wins, whichever driver owns it.
	touch(filepath.Join(top, "a", ".h"))
	if r, err = Detect(sub); err != nil || r.Root() != filepath.Join(top, "a") {
		t.Fatalf("nearest = %v, %v", r, err)
	}

	// Two drivers on one directory: name order decides ("g" before "h").
	touch(filepath.Join(top, "a", ".g"))
	g.opened, h.opened = nil, nil
	if _, err = Detect(sub); err != nil || len(g.opened) != 1 || len(h.opened) != 0 {
		t.Fatalf("name order: err %v, g %v, h %v", err, g.opened, h.opened)
	}

	// An Open failure is returned, not skipped.
	g.openErr = errors.New("corrupt")
	if _, err = Detect(sub); err == nil || err.Error() != "corrupt" {
		t.Fatalf("open error = %v", err)
	}
}

func TestDetectErrors(t *testing.T) {
	if _, err := Detect(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing path must be an error")
	}
	absFunc = func(string) (string, error) { return "", errors.New("no cwd") }
	defer func() { absFunc = filepath.Abs }()
	if _, err := Detect("rel"); err == nil {
		t.Error("an Abs failure must be returned")
	}
}
