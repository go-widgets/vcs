# vcs/git

The git driver for [go-widgets/vcs](https://github.com/go-widgets/vcs): a
separate module (`github.com/go-widgets/vcs/git`, tags `git/vX.Y.Z`) on
[go-git](https://github.com/go-git/go-git) v5, pure Go, so it also runs in
WebAssembly. It never runs the `git` binary. The core module links none of it.

```go
import (
    "github.com/go-widgets/vcs"
    _ "github.com/go-widgets/vcs/git" // registers "git"
)

repo, err := vcs.Detect(dir) // a directory holding .git, or any directory below one
states, err := repo.Status(ctx)
```

## go-git status to `vcs.State`

go-git reports two codes per path, as `git status --short` does: Staging (the
`X` column, HEAD against the index) and Worktree (`Y`, index against the files),
defined in [git-status(1), Short Format](https://git-scm.com/docs/git-status#_short_format).
One state is picked from the pair, first match wins:

| Rule (either column) | `vcs.State` |
| --- | --- |
| `U`, or `DD`, or `AA` (unmerged); any path in the index at merge stage 1, 2 or 3 | `Conflicted` |
| `?` | `Untracked` |
| `D` (so `AD` is a file that is gone) | `Deleted` |
| `R` | `Renamed` |
| `A` or `C` (so `AM` stays a new file) | `Added` |
| `M` | `Modified` |
| both unmodified | not in the map |

What go-git does and does not report: ignored paths are dropped by go-git
(`.gitignore` files; not `info/exclude` nor the global excludes file), so they are
never `Untracked` and `Ignored` is never returned; it has no rename detection,
so a rename is a `Deleted` old path and an `Added` new one; its Status never
yields `U`, so conflicts come from the index's merge stages.

## Bounds

go-git builds its whole Status in memory (and takes no context), so the driver
cannot bound that peak. It bounds what it returns: more than `Driver.MaxChanges`
entries (default 200 000, negative for none) is `ErrTooManyChanges`. The context is
checked before and after the call. Paths come from `.git/index`, which a hostile
repository controls: anything that is not a clean relative slash path is dropped
(fuzzed: `FuzzCleanPath`). Bare repositories are refused by `Open`.

## Gates

100 % statement coverage with repositories built by go-git in `t.TempDir()`,
vet including `GOOS=js GOARCH=wasm`, six Linux architectures, fuzz, govulncheck.
