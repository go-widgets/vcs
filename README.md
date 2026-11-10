# vcs

A small version-control abstraction for widgets: what state each file of a
working tree is in, and a `database/sql`-style registry of drivers that can
answer. Standard library only, so a toolkit can depend on it without linking any
version-control code.

```go
import (
    "context"

    "github.com/go-widgets/vcs"
    _ "github.com/go-widgets/vcs/git" // the git driver, a separate module
)

repo, err := vcs.Detect(dir) // walks up to the nearest repository, as git does
states, err := repo.Status(context.Background())
// states["themes/dark.css"] == vcs.Modified; a path not in the map is unchanged
```

## API

```go
type State int // Unchanged (zero), Modified, Added, Deleted, Renamed, Untracked, Ignored, Conflicted

type Repository interface {
    Root() string
    Status(ctx context.Context) (map[string]State, error) // slash paths relative to Root
}

type Driver interface {
    Recognises(dir string) bool // dir itself is the top of a working tree
    Open(dir string) (Repository, error)
}

func Register(name string, d Driver)        // panics on nil, "" or a duplicate
func Open(name, path string) (Repository, error)
func Detect(path string) (Repository, error) // ErrNoRepository when nothing is found
func Drivers() []string
```

`Detect` does the walking (the path, or its directory when it is a file, then
each parent), asks every registered driver in name order, and the nearest
directory wins. Later operations (add, remove, move, commit) will be separate
small interfaces a driver may implement and a caller finds by type assertion;
none is declared until a driver can test it.

## Packages

- `vcs`: the types and the registry.
- `vcstest`: a fake driver (`NewRepo`, `Set`, `Fail`, `NewDriver`) to test a
  consumer without any version-control system.
- `vcs/git` (separate module, `github.com/go-widgets/vcs/git`): the git driver,
  on go-git; the core links none of it.

## Gates

100 % statement coverage, vet including `GOOS=js GOARCH=wasm`, the six 64-bit
Linux architectures cross-compiled, 32-bit tests, govulncheck.
