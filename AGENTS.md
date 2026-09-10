# AGENTS.md

Guidance for AI agents and developers working on `smart-device-logger`.

## What this is

A terminal tool that streams a USB serial device to the screen and to one log
file per day. It is at the skeleton stage: the log path is written and tested,
the serial path is not. `README.md` has the current state and the planned
pieces; keep that table honest as work lands.

## Layout

Flat `package main`, one file per module. Prefer adding behaviour to an existing
file over creating a package for it — this is a single binary.

- `main.go` — flags, `signal.NotifyContext`, wiring. `main` only decides the
  exit code; everything else is in `run(ctx, args, stdin, stdout, stderr)` so
  the tests drive the real entry point.
- `stream.go` — `stream` reads lines and writes stamped lines. It takes an
  `io.Reader`, which is the seam the serial port will arrive at.
- `logfile.go` — `DailyWriter`. Owns the file name, the directory, the lazy
  open and the midnight rollover. Nothing else touches the log path.

## Rules that already matter

- **Append, never truncate.** A restart on the same day continues that day's
  file. Captured device data cannot be recovered by re-running.
- **Roll over on write, not on a timer.** An idle logger leaves no empty files.
- **Nothing on disk until the first line.** Constructing a `DailyWriter` has no
  side effects, so `--version` and a failed flag parse leave no directories.
- **Strip the device's line ending.** A CRLF device and an LF device must
  produce the same file.
- **A cancelled context is a clean stop**, not an error — Ctrl-C is the normal
  way to end a session.
- **Never commit captured data.** `.gitignore` excludes `/logs/` and `*.log`.

## Testing

Test-first. New behaviour arrives as a failing test in the same commit as the
code that passes it, or in the commit before it.

**Seams.** Tests go at three boundaries and nowhere else:

| Seam | What is tested there |
| --- | --- |
| `run` | flags, version, the screen-and-file pair, clean shutdown |
| `stream` | stamping, line endings, partial lines, read failures |
| `DailyWriter` | file naming, lazy creation, rollover, append, close |

`record` and `open` sit behind those seams. Reaching past a seam gives a test
that breaks on a refactor which changed no behaviour.

**Time is injected, never slept on.** `DailyWriter.now` and the `now` argument
to `stream` exist so a rollover test moves the clock two minutes instead of
waiting until midnight. Keep it that way for anything time-dependent.

**Coverage** is a ratchet with a floor of 80%, enforced by the `Coverage floor`
step in `ci.yml` and by the fifth command in the loop below. The suite reaches
about 89%. Raise the floor only after the suite has already cleared the new
level. The uncovered remainder is `main` and I/O failure paths — wiring with no
behaviour to assert. Do not chase it.

## Verification loop

**Run all six before every commit. This is the whole gate — do not substitute a
subset of it.**

```sh
gofmt -l .                     # must print nothing
go vet ./...
go tool staticcheck ./...
go test ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1
go tool govulncheck ./...
```

| Step | Command | Catches |
| --- | --- | --- |
| Format | `gofmt -l .` | `go vet` does **not** check formatting |
| Vet | `go vet ./...` | suspicious constructs |
| Lint | `go tool staticcheck ./...` | unused code, dead branches, simplifications |
| Test | `go test ./...` | behaviour |
| Coverage | `go tool cover -func` | untested code arriving (floor, see Testing) |
| Vulnerabilities | `go tool govulncheck ./...` | known advisories in dependencies |

`gofmt -l` only lists offending files — it exits 0 either way, so treat any
output as a failure. `gofmt -l -w .` formats them in place.

The `verify` job in `.github/workflows/ci.yml` is the source of truth for this
list. If the two ever disagree, CI wins.

Three checks are **CI-only**, deliberately:

- `go test -race ./...` — the race detector needs cgo.
- `go mod tidy` leaving no diff.
- Cross-compilation of windows/linux/darwin × amd64/arm64.

`govulncheck` also runs weekly in `vuln.yml`, because a new advisory can affect
an unchanged commit.

## Toolchain

**Go is the only requirement.** The version is pinned by the `go` directive in
`go.mod`; `GOTOOLCHAIN=auto` fetches exactly that version.

`staticcheck` and `govulncheck` are `tool` directives in `go.mod`, so
`go tool staticcheck` and `go tool govulncheck` work from a bare Go install and
CI needs no install step. Bump them with `go get -tool <path>@latest`.

Build with the version stamped in:

```sh
go build -ldflags "-X main.version=$(git describe --tags --always)" -o smart-device-logger .
```

## Conventions

- Commits follow Conventional Commits.
- Keep the verification loop above and the `verify` job in `ci.yml` in step. If
  you add a check to one, add it to the other, or the documented gate stops
  meaning anything.
- Do not add a task runner, wrapper script, or tool manager as a required step.
  Go alone must be enough to build, test and verify this repo.
- Keep the binary pure Go (`CGO_ENABLED=0`) so it cross-compiles to every
  machine a device might be plugged into. Choose serial libraries accordingly.
