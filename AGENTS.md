# AGENTS.md

Guidance for AI agents and developers working on `smart-device-logger`.

## What this is

A terminal tool that streams a USB serial device to the screen and to one log
file per day. It is at the skeleton stage: the log path is written and tested,
the serial path is not. `README.md` has the current state; keep that table
honest as work lands.

## Read this before building anything

`.wayfinder/serial-logger/ANSWER-KEY.md` is the standard the finished work is
judged against: 26 binary checks, an out-of-scope list, and three named
Unknowns that must not be guessed at. `MAP.md` beside it holds the reasoning
behind every check, and a **Facts established** section with measured values
and citations — read that before assuming anything about the device, the
library, or the kernel.

`PLAN.md` predates both and is wrong in places the map corrects. Where they
disagree, the map wins.

**The current `stream.go` cannot capture the target device.** It is built on
`bufio.Reader`, which aborts with `io.ErrNoProgress` after 100 consecutive
empty reads — 20 seconds at a 200 ms read timeout, against a device that is
normally silent for up to 30 minutes. Replacing it is the first job, not a
refinement.

## Layout

`package main` at the root, one file per module. Prefer adding behaviour to an
existing file over creating a package — this is a single binary. `internal/`
and `cmd/` exist only for test scaffolding.

- `main.go` — flags, `signal.NotifyContext`, wiring. `main` only decides the
  exit code; everything else is in `run(ctx, args, stdin, stdout, stderr)` so
  the tests drive the real entry point.
- `stream.go` — `stream` reads lines and writes stamped lines. It takes an
  `io.Reader`, which is the seam the serial port will arrive at. See the
  warning above.
- `logfile.go` — `DailyWriter`. Owns the file name, the directory, the lazy
  open and the midnight rollover. Nothing else touches the log path.
- `version.go` — what `--version` reports, from ldflags or
  `debug.ReadBuildInfo`.
- `internal/fakedev` — a serial device with no hardware: a pseudo-terminal
  whose slave path opens like `/dev/ttyUSB0`, plus `Boot`, the real
  VoidCellular transcript. Not shipped in the binary.
- `cmd/fakedev` — the same thing as a command, for driving the real binary by
  hand.

## Rules that already matter

- **Append, never truncate.** A restart on the same day continues that day's
  file. Captured device data cannot be recovered by re-running.
- **Roll over on write, not on a timer.** An idle logger leaves no empty files.
- **Nothing on disk until the first line.** Constructing a `DailyWriter` has no
  side effects, so `--version` and a failed flag parse leave no directories.
- **Three line endings, one log.** `\r\n`, a lone `\n` and a lone `\r` all end
  a line, and the terminator is stripped. The device emits all three — CRLF on
  boot lines, a bare LF on the `mreport` CSV header, lone CR in the AT
  exchange.
- **Silence never ends a capture.** Gaps of up to 30 minutes are normal.
- **A cancelled context is a clean stop**, not an error — Ctrl-C is the normal
  way to end a session.
- **Never commit captured data.** `.gitignore` excludes `/logs/` and `*.log`.

## Testing

Test-first. New behaviour arrives as a failing test in the same commit as the
code that passes it, or in the commit before it.

**Seams.** Tests go at these boundaries and nowhere else:

| Seam | What is tested there |
| --- | --- |
| `run` | flags, version, the screen-and-file pair, clean shutdown |
| `stream` | stamping, line endings, partial lines, read failures |
| `DailyWriter` | file naming, lazy creation, rollover, append, close |
| `fakedev.Device` | that the rig itself delivers bytes verbatim and hangs up on unplug |

`record` and `open` sit behind those seams. Reaching past a seam gives a test
that breaks on a refactor which changed no behaviour.

**Test against the fake device, not against hardware.** `internal/fakedev`
gives a real character device with no cable attached, so line endings,
unterminated fragments, long silences and unplugs are all reproducible in a
unit test:

```go
dev, err := fakedev.Open()   // dev.Path is e.g. /dev/pts/7
dev.Send("A\rB\r\nC\n")      // exactly what the wire carries
dev.Unplug()                 // hangs the reader up like pulling the lead
```

`fakedev.Boot` is the real VoidCellular transcript, chunked as the device
chunks it. Use it rather than inventing sample output — it is the only sample
that contains every awkward case at once.

**Time is injected, never slept on.** `DailyWriter.now` and the `now` argument
to `stream` exist so a rollover test moves the clock two minutes instead of
waiting until midnight. Keep it that way for anything time-dependent.

**Coverage** is a ratchet with a floor of 80%, enforced by the `Coverage floor`
step in `ci.yml` and by the fifth command in the loop below. It measures `.`
and `./internal/...`; `./cmd/...` is excluded as argv wiring, so a test rig's
`main()` cannot drag the ratchet down. The suite reaches about 88%. Raise the
floor only after the suite has already cleared the new level. The uncovered
remainder is `main` and I/O failure paths — wiring with no behaviour to
assert. Do not chase it.

## Verification loop

**Run all six before every commit. This is the whole gate — do not substitute a
subset of it.**

```sh
gofmt -l .                     # must print nothing
go vet ./...
go tool staticcheck ./...
go test ./...
go test -coverprofile=coverage.out . ./internal/... && go tool cover -func=coverage.out | tail -1
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
- Cross-compilation of the Linux targets: amd64, arm64, armv7, armv6.

`govulncheck` also runs weekly in `vuln.yml`, because a new advisory can affect
an unchanged commit.

### The gate is not proof that it works

The six commands prove the code is clean and the tests pass. They do not
prove the tool logs a device. Before claiming a piece of the serial work is
done, drive the real binary from the fake device and read the output:

```sh
go run ./cmd/fakedev &          # prints a device path
go build -o /tmp/sdl . && /tmp/sdl --port <that path>
```

Then run the checks in `.wayfinder/serial-logger/ANSWER-KEY.md` that the
change touches. Twenty-five of the twenty-six need no hardware.

## Toolchain

**Go is the only requirement.** The version is pinned by the `go` directive in
`go.mod`; `GOTOOLCHAIN=auto` fetches exactly that version.

`staticcheck` and `govulncheck` are `tool` directives in `go.mod`, so
`go tool staticcheck` and `go tool govulncheck` work from a bare Go install and
CI needs no install step. Bump them with `go get -tool <path>@latest`.

`go build` alone is enough. Do not pass `-ldflags` by hand: `version.go` reads
`debug.ReadBuildInfo`, so a local build already reports its commit and whether
the tree was dirty. `-X main.version=<tag>` is set by the release workflow and
nowhere else.

## Releases

Push a tag; `.github/workflows/release.yml` does the rest — the gate, then
static linux/arm64, armv7, armv6 and amd64 binaries plus `checksums.txt`,
published with install instructions in the notes.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The Pi has no Go toolchain and is not getting one: it installs a released
binary with `curl`. So a dependency that is not pure Go, or that does not
cross-compile to arm, is not an option — the release cannot be produced.

Rehearse a change to the build steps before tagging by running them locally,
and run the arm binaries under `qemu-arm-static` to check they are not just
link-clean but actually start.

## Conventions

- Commits follow Conventional Commits.
- Keep the verification loop above and the `verify` job in `ci.yml` in step. If
  you add a check to one, add it to the other, or the documented gate stops
  meaning anything.
- Do not add a task runner, wrapper script, or tool manager as a required step.
  Go alone must be enough to build, test and verify this repo.
- **Linux only.** The tool runs on Raspberry Pis and on an x86 Linux dev box.
  Do not add Windows or macOS support, build targets, or conditional code, and
  do not write `COM3` in an example.
- Keep the binary pure Go (`CGO_ENABLED=0`) so a Pi binary cross-compiles from
  the dev box with no toolchain to install. `go.bug.st/serial` and its
  `enumerator` package are pure Go on Linux — verified: `CGO_ENABLED=0
  GOOS=linux` builds `enumerator.GetDetailedPortsList`. Choose any further
  serial dependency the same way.
- **Never edit the answer key to match the code.** A check that fails means
  the code is wrong, or the decision behind it changed — and a changed
  decision is rewritten in `MAP.md`, with its reasoning, before the check
  moves. Silently relaxing a check destroys the only standard there is.
- **Never answer an Unknown by guessing.** `U1`, `U2` and `U3` in the answer
  key are undecided, not unspecified. Work that touches one stops and asks.
- Anything in the out-of-scope list makes the result **worse**, not richer.
  Sending data to the device, a config file, retention, a second device, and
  protocol decoding are all excluded deliberately.
