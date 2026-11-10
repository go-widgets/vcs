// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

// Package vcs is a small version-control abstraction for widgets: what state
// each file of a working tree is in, and a registry of drivers that can answer.
// It imports the standard library only, so a widget toolkit can depend on it
// without linking any version-control code; a driver (github.com/go-widgets/vcs/git
// for git) is a separate module that the application imports for its side
// effect, as with database/sql:
//
//	import _ "github.com/go-widgets/vcs/git"
//
//	repo, err := vcs.Detect(dir)
//	states, err := repo.Status(ctx)
//
// # Room for more
//
// [Repository] is deliberately only what a file explorer needs: a root and a
// status. Later operations (add, remove, move, commit) belong in separate small
// interfaces that a driver MAY implement and a caller discovers by type
// assertion, the way io.WriterTo extends io.Reader:
//
//	if a, ok := repo.(interface{ Add(ctx context.Context, paths ...string) error }); ok { … }
//
// None is declared here until a driver can implement and test it.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// State is a file's state in a working tree. The zero value is [Unchanged], so a
// map lookup for a path the repository does not mention reads "nothing to see".
//
// The set is the vocabulary git, Mercurial, Subversion, Jujutsu and Fossil share.
// It maps one to one onto go-widgets/toolkit's FileStatus, except [Renamed] (the
// toolkit draws it as an added file at the new path) and [Ignored] (no mark).
type State int

const (
	// Unchanged is tracked and identical to the last commit.
	Unchanged State = iota
	// Modified is tracked and edited.
	Modified
	// Added is newly tracked and not yet committed.
	Added
	// Deleted is tracked and gone from the working tree (or staged for removal).
	Deleted
	// Renamed is tracked under a new path; the key of the status map is the NEW path.
	Renamed
	// Untracked is present, not ignored, and not under version control.
	Untracked
	// Ignored is present and excluded by the repository's ignore rules.
	Ignored
	// Conflicted needs a human: a merge left both sides in it.
	Conflicted
)

var stateNames = [...]string{"unchanged", "modified", "added", "deleted", "renamed", "untracked", "ignored", "conflicted"}

// String is the lower-case name of s, or "State(n)" for a value outside the set.
func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return fmt.Sprintf("State(%d)", int(s))
	}
	return stateNames[s]
}

// Repository is a working tree under version control.
type Repository interface {
	// Root is the absolute, OS-native path of the working tree's top directory.
	Root() string
	// Status reports every path that is not [Unchanged], as slash-separated paths
	// relative to [Repository.Root] ("themes/dark.css", never "./" nor a leading
	// slash). A path absent from the map is unchanged. It honours ctx: a cancelled
	// context returns its error rather than a partial map.
	Status(ctx context.Context) (map[string]State, error)
}

// Driver recognises and opens one kind of repository.
type Driver interface {
	// Recognises reports whether dir ITSELF is the top of one of this driver's
	// working trees (git: it holds a .git). It must not look at the parents:
	// [Detect] does the walking, so every driver sees the same directories in the
	// same order and the nearest repository wins whichever driver owns it.
	Recognises(dir string) bool
	// Open opens the working tree whose top directory is dir.
	Open(dir string) (Repository, error)
}

// ErrNoRepository is returned by [Detect] when no registered driver recognises
// the directory or any of its parents.
var ErrNoRepository = errors.New("vcs: no repository found")

// absFunc is filepath.Abs, replaceable because its only failure (no working
// directory) cannot be provoked portably.
var absFunc = filepath.Abs

var (
	mu      sync.RWMutex
	drivers = map[string]Driver{}
)

// Register makes a driver available under name. Like database/sql.Register it
// panics on a nil driver, an empty name or a name registered twice, because the
// only caller is an init function and the mistake is the programmer's.
func Register(name string, d Driver) {
	mu.Lock()
	defer mu.Unlock()
	if d == nil {
		panic("vcs: Register driver is nil")
	}
	if name == "" {
		panic("vcs: Register name is empty")
	}
	if _, dup := drivers[name]; dup {
		panic("vcs: Register called twice for driver " + name)
	}
	drivers[name] = d
}

// Unregister removes a driver; it is for tests that register a fake and must
// leave the registry as they found it. Unknown names are ignored.
func Unregister(name string) {
	mu.Lock()
	defer mu.Unlock()
	delete(drivers, name)
}

// Drivers lists the registered driver names, sorted.
func Drivers() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(drivers))
	for n := range drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Open opens the working tree at path (its top directory) with the named driver.
func Open(name, path string) (Repository, error) {
	mu.RLock()
	d := drivers[name]
	mu.RUnlock()
	if d == nil {
		return nil, fmt.Errorf("vcs: unknown driver %q (forgotten import?)", name)
	}
	abs, err := absFunc(path)
	if err != nil {
		return nil, err
	}
	return d.Open(abs)
}

// Detect finds the repository holding path: path (or its directory, when it is a
// file) and then each parent up to the filesystem root, asking every registered
// driver, in name order, whether it recognises that directory. The nearest
// directory wins, so a git checkout inside another driver's tree is reported as
// the git one. It returns [ErrNoRepository] when nothing is found.
func Detect(path string) (Repository, error) {
	dir, err := absFunc(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	mu.RLock()
	names := make([]string, 0, len(drivers))
	for n := range drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	ds := make([]Driver, len(names))
	for i, n := range names {
		ds[i] = drivers[n]
	}
	mu.RUnlock()
	for {
		for _, d := range ds {
			if d.Recognises(dir) {
				return d.Open(dir)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, ErrNoRepository
		}
		dir = parent
	}
}
