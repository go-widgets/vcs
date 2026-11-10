// Copyright (c) 2026 the go-widgets/vcs authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package git

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-widgets/vcs"
)

// fixture is a real repository made with go-git itself: no git binary.
type fixture struct {
	t    *testing.T
	dir  string
	repo *gogit.Repository
	wt   *gogit.Worktree
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dir: dir, repo: r, wt: wt}
}

func (f *fixture) write(name, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.dir, filepath.FromSlash(name))); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) add(names ...string) {
	f.t.Helper()
	for _, n := range names {
		if _, err := f.wt.Add(n); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) commit(msg string) {
	f.t.Helper()
	sig := &object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(1_700_000_000, 0)}
	if _, err := f.wt.Commit(msg, &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) open() vcs.Repository {
	f.t.Helper()
	r, err := Driver{}.Open(f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) status() map[string]vcs.State {
	f.t.Helper()
	got, err := f.open().Status(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// committed is a repository with one commit holding a.txt, b.txt, sub/c.txt.
func committed(t *testing.T) *fixture {
	f := newFixture(t)
	f.write("a.txt", "a")
	f.write("b.txt", "b")
	f.write("sub/c.txt", "c")
	f.add("a.txt", "b.txt", "sub/c.txt")
	f.commit("init")
	return f
}

func expect(t *testing.T, got, want map[string]vcs.State) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Errorf("status = %v, want %v", got, want)
	}
}

func TestStatusStates(t *testing.T) {
	f := committed(t)
	expect(t, f.status(), map[string]vcs.State{})

	f.write("a.txt", "changed") // modified in the worktree
	expect(t, f.status(), map[string]vcs.State{"a.txt": vcs.Modified})
	f.add("a.txt") // modified in the index: still Modified
	expect(t, f.status(), map[string]vcs.State{"a.txt": vcs.Modified})

	f.remove("b.txt") // deleted in the worktree
	f.write("new.txt", "n")
	f.write("sub/deep/new2.txt", "n")
	expect(t, f.status(), map[string]vcs.State{
		"a.txt": vcs.Modified, "b.txt": vcs.Deleted,
		"new.txt": vcs.Untracked, "sub/deep/new2.txt": vcs.Untracked,
	})

	f.add("new.txt") // untracked becomes added
	f.write("new.txt", "edited after add")
	got := f.status()
	if got["new.txt"] != vcs.Added { // "AM": a new file stays Added
		t.Errorf("new.txt = %v, want Added", got["new.txt"])
	}

	if _, err := f.wt.Remove("sub/c.txt"); err != nil { // staged removal
		t.Fatal(err)
	}
	if got = f.status(); got["sub/c.txt"] != vcs.Deleted {
		t.Errorf("sub/c.txt = %v, want Deleted", got["sub/c.txt"])
	}
}

func TestStatusIgnored(t *testing.T) {
	f := newFixture(t)
	f.write(".gitignore", "*.log\nbuild/\n")
	f.add(".gitignore")
	f.commit("ignore")
	f.write("keep.txt", "k")
	f.write("debug.log", "ignored")
	f.write("build/out.bin", "ignored")
	f.write("sub/x.log", "ignored too")
	expect(t, f.status(), map[string]vcs.State{"keep.txt": vcs.Untracked})
}

func TestStatusRename(t *testing.T) {
	f := committed(t)
	if _, err := f.wt.Move("a.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	// go-git has no rename detection: the old path is Deleted, the new one Added.
	expect(t, f.status(), map[string]vcs.State{"a.txt": vcs.Deleted, "renamed.txt": vcs.Added})
}

func TestStatusEmptyRepository(t *testing.T) {
	f := newFixture(t)
	expect(t, f.status(), map[string]vcs.State{})
	f.write("first.txt", "1")
	expect(t, f.status(), map[string]vcs.State{"first.txt": vcs.Untracked})
}

// conflict marks c.txt as unmerged the way `git merge` leaves the index:
// the same path at stages 1 (ancestor), 2 (ours) and 3 (theirs).
func (f *fixture) conflict(name string) {
	f.t.Helper()
	idx, err := f.repo.Storer.Index()
	if err != nil {
		f.t.Fatal(err)
	}
	var kept []*index.Entry
	var base *index.Entry
	for _, e := range idx.Entries {
		if e.Name == name {
			base = e
			continue
		}
		kept = append(kept, e)
	}
	for stage := 1; stage <= 3; stage++ {
		e := *base
		e.Stage = index.Stage(stage)
		kept = append(kept, &e)
	}
	idx.Entries = kept
	if err := f.repo.Storer.SetIndex(idx); err != nil {
		f.t.Fatal(err)
	}
}

func TestStatusConflict(t *testing.T) {
	f := committed(t)
	f.conflict("sub/c.txt")
	got := f.status()
	if got["sub/c.txt"] != vcs.Conflicted {
		t.Errorf("sub/c.txt = %v, want Conflicted (status %v)", got["sub/c.txt"], got)
	}
}

func TestStatusDropsHostilePaths(t *testing.T) {
	f := committed(t)
	for _, bad := range []string{"../evil", "/abs", "a/../b", ""} {
		idx, _ := f.repo.Storer.Index()
		for stage := 2; stage <= 3; stage++ {
			idx.Entries = append(idx.Entries, &index.Entry{Name: bad, Stage: index.Stage(stage), Hash: plumbing.ZeroHash, Mode: 0o100644})
		}
		if err := f.repo.Storer.SetIndex(idx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.open().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for p := range got {
		if _, ok := cleanPath(p); !ok {
			t.Errorf("hostile path %q reached the result", p)
		}
	}
}

func TestStatusLimit(t *testing.T) {
	f := committed(t)
	f.write("u1.txt", "")
	f.write("u2.txt", "")
	f.write("u3.txt", "")

	r, err := Driver{MaxChanges: 2}.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Status(context.Background()); !errors.Is(err, ErrTooManyChanges) {
		t.Errorf("limit 2 over 3 changes: err = %v", err)
	}
	if r, _ = (Driver{MaxChanges: 3}).Open(f.dir); r != nil {
		if got, err := r.Status(context.Background()); err != nil || len(got) != 3 {
			t.Errorf("limit 3 over 3 changes: %v, %v", got, err)
		}
	}
	if r, _ = (Driver{MaxChanges: -1}).Open(f.dir); r != nil {
		if got, err := r.Status(context.Background()); err != nil || len(got) != 3 {
			t.Errorf("no limit: %v, %v", got, err)
		}
	}
	// The limit also bounds conflicts, and a path counted twice counts once.
	f.conflict("a.txt")
	f.conflict("b.txt")
	r, _ = Driver{MaxChanges: 3}.Open(f.dir)
	if _, err = r.Status(context.Background()); !errors.Is(err, ErrTooManyChanges) {
		t.Errorf("conflicts over the limit: err = %v", err)
	}
	if d := (Driver{}); d.MaxChanges != 0 {
		t.Fatal("zero Driver must use the default")
	}
	def, _ := Driver{}.Open(f.dir)
	if def.(*Repo).max != DefaultMaxChanges {
		t.Errorf("default limit = %d", def.(*Repo).max)
	}
}

// lateCancel reports no error on the first Err call and Canceled afterwards,
// to cancel between the two checks Status makes.
type lateCancel struct {
	context.Context
	calls int
}

func (c *lateCancel) Err() error {
	c.calls++
	if c.calls > 1 {
		return context.Canceled
	}
	return nil
}

func TestStatusContext(t *testing.T) {
	f := committed(t)
	r := f.open()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before: %v", err)
	}
	if _, err := r.Status(&lateCancel{Context: context.Background()}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled during: %v", err)
	}
}

func TestStatusErrors(t *testing.T) {
	// A corrupt index.
	f := committed(t)
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "index"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.open().Status(context.Background()); err == nil {
		t.Error("a corrupt index must be an error")
	}

	// A branch pointing at a commit that is not there: the index reads, the diff fails.
	g := committed(t)
	ref := filepath.Join(g.dir, ".git", "refs", "heads", "master")
	if _, err := os.Stat(ref); err != nil {
		ref = filepath.Join(g.dir, ".git", "refs", "heads", "main")
	}
	if err := os.WriteFile(ref, []byte("0123456789012345678901234567890123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.open().Status(context.Background()); err == nil {
		t.Error("a dangling HEAD must be an error")
	}
}

func TestBareRepositoryHasNoStatus(t *testing.T) {
	dir := t.TempDir()
	bare, err := gogit.PlainInit(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Driver{}).Open(dir); !errors.Is(err, gogit.ErrIsBareRepository) {
		t.Errorf("Open(bare) = %v, want ErrIsBareRepository", err)
	}
	r := &Repo{root: dir, repo: bare, max: 1}
	if _, err := r.Status(context.Background()); !errors.Is(err, gogit.ErrIsBareRepository) {
		t.Errorf("Status(bare) = %v, want ErrIsBare", err)
	}
	if r.Root() != dir {
		t.Errorf("Root = %q", r.Root())
	}
}

func TestOpenNotARepository(t *testing.T) {
	if _, err := (Driver{}).Open(t.TempDir()); err == nil {
		t.Error("Open on a plain directory must fail")
	}
}

func TestRegisteredAndDetect(t *testing.T) {
	found := false
	for _, n := range vcs.Drivers() {
		found = found || n == "git"
	}
	if !found {
		t.Fatal(`the "git" driver is not registered`)
	}
	f := committed(t)
	f.write("sub/deeper/x.txt", "x")
	for _, from := range []string{f.dir, filepath.Join(f.dir, "sub", "deeper"), filepath.Join(f.dir, "sub", "deeper", "x.txt")} {
		r, err := vcs.Detect(from)
		if err != nil || r.Root() != f.dir {
			t.Fatalf("Detect(%s) = %v, %v", from, r, err)
		}
	}
	r, err := vcs.Open("git", f.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Status(context.Background())
	if err != nil || got["sub/deeper/x.txt"] != vcs.Untracked {
		t.Errorf("through the registry: %v, %v", got, err)
	}
	if _, err := vcs.Detect(t.TempDir()); err == nil {
		// A temp dir has no .git above it unless the host's TMPDIR is inside a
		// checkout; only the error type matters when it is not found.
		t.Log("TMPDIR sits inside a git checkout; ErrNoRepository not exercised")
	} else if !errors.Is(err, vcs.ErrNoRepository) {
		t.Errorf("Detect outside any repository: %v", err)
	}
}

func TestRecognises(t *testing.T) {
	f := newFixture(t)
	if !(Driver{}).Recognises(f.dir) {
		t.Error(".git directory not recognised")
	}
	plain := t.TempDir()
	if (Driver{}).Recognises(plain) {
		t.Error("a plain directory is not a repository")
	}
	// A linked worktree or submodule has a .git FILE.
	if err := os.WriteFile(filepath.Join(plain, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !(Driver{}).Recognises(plain) {
		t.Error(".git file not recognised")
	}
}

func TestStateOf(t *testing.T) {
	// Every pair of git-status(1) "Short Format" codes go-git can produce, plus
	// the unmerged pairs of the table.
	cases := []struct {
		staging, worktree byte
		want              vcs.State
		changed           bool
	}{
		{' ', ' ', vcs.Unchanged, false},
		{'M', ' ', vcs.Modified, true},
		{' ', 'M', vcs.Modified, true},
		{'M', 'M', vcs.Modified, true},
		{'A', ' ', vcs.Added, true},
		{'A', 'M', vcs.Added, true},
		{'C', ' ', vcs.Added, true},
		{' ', 'C', vcs.Added, true},
		{'D', ' ', vcs.Deleted, true},
		{' ', 'D', vcs.Deleted, true},
		{'A', 'D', vcs.Deleted, true},
		{'M', 'D', vcs.Deleted, true},
		{'R', ' ', vcs.Renamed, true},
		{'R', 'M', vcs.Renamed, true},
		{' ', 'R', vcs.Renamed, true},
		{'?', '?', vcs.Untracked, true},
		{' ', '?', vcs.Untracked, true},
		{'U', 'U', vcs.Conflicted, true},
		{'A', 'U', vcs.Conflicted, true},
		{'U', 'D', vcs.Conflicted, true},
		{'U', 'A', vcs.Conflicted, true},
		{'D', 'U', vcs.Conflicted, true},
		{'D', 'D', vcs.Conflicted, true},
		{'A', 'A', vcs.Conflicted, true},
	}
	for _, c := range cases {
		got, changed := StateOf(c.staging, c.worktree)
		if got != c.want || changed != c.changed {
			t.Errorf("StateOf(%q,%q) = %v,%v; want %v,%v", c.staging, c.worktree, got, changed, c.want, c.changed)
		}
	}
}

func TestCleanPath(t *testing.T) {
	good := []string{"a", "a/b.txt", "dir/with space/f", ".hidden", "a\\b", "..a", "a..", "a/.b"}
	bad := []string{"", ".", "..", "../x", "/abs", "a/../b", "a//b", "a/", "./a", "a/.", "a\x00b", "a/.."}
	for _, p := range good {
		if got, ok := cleanPath(p); !ok || got != p {
			t.Errorf("cleanPath(%q) = %q,%v; want accepted", p, got, ok)
		}
	}
	for _, p := range bad {
		if _, ok := cleanPath(p); ok {
			t.Errorf("cleanPath(%q) accepted", p)
		}
	}
}

func FuzzCleanPath(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "/x", "a//b", "a/./b", "", "a\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		got, ok := cleanPath(p)
		if !ok {
			return
		}
		if got != p || p == "" || p[0] == '/' {
			t.Fatalf("accepted %q as %q", p, got)
		}
		for _, seg := range splitSlash(p) {
			if seg == "" || seg == "." || seg == ".." {
				t.Fatalf("accepted %q with segment %q", p, seg)
			}
		}
	})
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return out
}
