# Contributing to FindIt

Thanks for helping! FindIt's goal is to make data recovery safe and
understandable for people who aren't experts, so clarity and safety beat
features.

## Setup

Requirements: macOS 12+, Go (version in `go.mod`), Node 20.19+ or 22.12+, and the
[Wails v2](https://wails.io) CLI.

```bash
brew install sleuthkit                                # engines used in development
go install github.com/wailsapp/wails/v2/cmd/wails@latest
npm --prefix frontend ci && npm --prefix frontend run build   # Go embeds frontend/dist
scripts/make_fixture.sh && scripts/make_fs_fixture.sh        # optional: test disk images
```

- `wails dev` runs the app with hot reload.
- `go test ./...` runs the suite. Tests that need a fixture image skip when it's
  missing; generate fixtures to run everything.
- `scripts/build_dmg.sh` builds a self-contained `build/FindIt.dmg`.

Reading a real drive needs the privileged helpers, which only exist in the
packaged app. In `wails dev`, open a disk image instead.

## Layout

| Path | Purpose |
| --- | --- |
| `app.go`, `main.go` | Wails bindings: the only API the UI calls |
| `internal/recovery` | Backend facade (scan, preview, recover) |
| `internal/session` | Orchestrates one scan: diagnosis → filesystems → raw carve |
| `internal/diagnosis` | Current / previous filesystem detection |
| `internal/carve` | Signature-based raw recovery (add a type with `carve.Register`) |
| `internal/engines/tsk` | The Sleuth Kit adapter (subprocess, validated requests) |
| `internal/storage` | The single read-only access path to source bytes |
| `internal/extract` | The single place that writes recovered files |
| `internal/device`, `internal/imaging`, `internal/privdev` | Drives, cloning, privileged helper protocol |
| `cmd/` | Privileged helpers and a headless diagnose CLI |
| `frontend/` | Vue 3 + Tailwind UI |

## Pull requests

- Keep `gofmt`, `go vet ./...`, and `staticcheck ./...` clean; CI runs them.
- Add or update tests for behaviour changes. Bugs found on real media are best
  reproduced with a small synthetic image built in the test.
- Never add a code path that writes to the source; read through
  `storage.Source`.
- Anything that runs as root must take structured, validated input.
- User-facing text should be plain English without recovery jargon.

By contributing you agree that your contributions are licensed under the
Apache License 2.0.
