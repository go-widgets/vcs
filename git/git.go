// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

// Package git is the git driver for [github.com/go-widgets/vcs]. Import it for
// its side effect:
//
//	import _ "github.com/go-widgets/vcs/git"
//
// It is built on go-git (pure Go, so it also runs in WebAssembly) and NEVER runs
// the git binary.
//
// # How a file's state is decided
//
// go-git reports two codes per changed path, as `git status --short` does:
// Staging (the "X" column, HEAD against the index) and Worktree (the "Y" column,
// index against the files). git-status(1) "Short Format"
// (https://git-scm.com/docs/git-status#_short_format) defines their meaning;
// [StateOf] picks ONE [vcs.State] from the pair, first match wins:
//
//	Conflicted  any column U (updated but unmerged), or DD or AA; and every path
//	            the index holds at a merge stage 1, 2 or 3 (see below)
//	Untracked   either column '?'
//	Deleted     either column D
//	Renamed     either column R
//	Added       either column A or C (a copy is a new path)
//	Modified    either column M
//	(none)      both columns unmodified: the path is left out of the map
//
// Deleted outranks Added because "AD" is a file that no longer exists; Conflicted
// outranks everything because it is the one state that needs a person.
//
// # What go-git does and does not report
//
//   - Ignored: go-git drops paths matched by .gitignore (the repository's and its
//     subdirectories') from the status entirely, so an ignored file is never
//     [vcs.Untracked] and [vcs.Ignored] is never returned. info/exclude and the
//     user's global excludes file are NOT read.
//   - Renamed: go-git has no rename detection in Status; a rename shows as a
//     Deleted old path and an Added (or Untracked) new one. The mapping keeps
//     'R' so a newer go-git is handled.
//   - Conflicted: go-git's Status never produces 'U'. The driver therefore also
//     reads the index and marks every path held at merge stage 1, 2 or 3.
//     (go-git's own constants are misleading: index.Merged is 1, but a merged
//     entry's raw stage is 0.)
//   - Submodules: reported as go-git reports them (a modified submodule is
//     Modified); their inner files are not listed.
//
// # Resource bounds
//
// go-git's Status takes no context and builds its whole result in memory before
// returning: the index is decoded, the working tree is walked, and one map
// entry exists per changed or untracked path. The driver cannot bound that peak.
// It bounds what it keeps and returns: more than [Driver.MaxChanges] entries
// (default [DefaultMaxChanges]) is [ErrTooManyChanges], not a huge map, so a
// consumer's memory is bounded by its own limit rather than by a hostile
// repository. The context is checked before and after the call, not during it.
// Paths come out of .git/index, which a hostile repository controls: a path that
// is not clean, relative and slash-separated (absolute, "..", empty element,
// NUL) is dropped, never passed on.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-widgets/vcs"
)

// DefaultMaxChanges is the limit used when [Driver.MaxChanges] is zero.
const DefaultMaxChanges = 200_000

// ErrTooManyChanges is returned by Status when the working tree has more
// changed paths than the driver's limit.
var ErrTooManyChanges = errors.New("vcs/git: too many changed paths")

func init() { vcs.Register("git", Driver{}) }

// Driver is the git [vcs.Driver], registered as "git".
type Driver struct {
	// MaxChanges bounds the entries Status returns; zero means
	// [DefaultMaxChanges], negative means no limit.
	MaxChanges int
}

// Recognises reports whether dir holds a .git: a directory, or the file
// ("gitdir: …") a linked worktree or submodule has.
func (Driver) Recognises(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// Open opens the non-bare repository whose working tree is dir.
func (d Driver) Open(dir string) (vcs.Repository, error) {
	r, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("vcs/git: %w", err)
	}
	if _, err := r.Worktree(); err != nil { // gogit.ErrIsBareRepository
		return nil, fmt.Errorf("vcs/git: %w", err)
	}
	limit := d.MaxChanges
	if limit == 0 {
		limit = DefaultMaxChanges
	}
	return &Repo{root: dir, repo: r, max: limit}, nil
}

// Repo is an open git working tree.
type Repo struct {
	root string
	repo *gogit.Repository
	max  int
}

// Root implements [vcs.Repository].
func (r *Repo) Root() string { return r.root }

// Status implements [vcs.Repository] with the rules in the package comment.
func (r *Repo) Status(ctx context.Context) (map[string]vcs.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx, err := r.repo.Storer.Index()
	if err != nil {
		return nil, fmt.Errorf("vcs/git: index: %w", err)
	}
	wt, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("vcs/git: %w", err)
	}
	st, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("vcs/git: status: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string]vcs.State{}
	add := func(name string, s vcs.State) error {
		p, ok := cleanPath(name)
		if !ok {
			return nil
		}
		if _, seen := out[p]; !seen && r.max > 0 && len(out) >= r.max {
			return fmt.Errorf("%w (limit %d)", ErrTooManyChanges, r.max)
		}
		out[p] = s
		return nil
	}
	for name, fs := range st {
		if s, changed := StateOf(byte(fs.Staging), byte(fs.Worktree)); changed {
			if err := add(name, s); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range idx.Entries {
		if e.Stage != 0 { // raw stage 0 is merged; 1, 2, 3 are the sides of a conflict
			if err := add(e.Name, vcs.Conflicted); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// StateOf picks the [vcs.State] of a path from go-git's two status codes, given
// as their git-status(1) characters (' ', '?', 'M', 'A', 'D', 'R', 'C', 'U'),
// and reports false when both are unmodified. See the package comment for the
// table.
func StateOf(staging, worktree byte) (vcs.State, bool) {
	either := func(c byte) bool { return staging == c || worktree == c }
	switch {
	case either('U'), staging == 'D' && worktree == 'D', staging == 'A' && worktree == 'A':
		return vcs.Conflicted, true
	case either('?'):
		return vcs.Untracked, true
	case either('D'):
		return vcs.Deleted, true
	case either('R'):
		return vcs.Renamed, true
	case either('A'), either('C'):
		return vcs.Added, true
	case either('M'):
		return vcs.Modified, true
	}
	return vcs.Unchanged, false
}

// cleanPath accepts a slash-separated path relative to the root exactly as it
// should be reported: non-empty, no NUL, not absolute, and already clean (no
// ".", "..", empty or trailing-slash elements). Anything else is not a file of
// the working tree.
func cleanPath(p string) (string, bool) {
	if p == "" || strings.ContainsRune(p, 0) || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}
