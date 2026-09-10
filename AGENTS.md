# AGENTS.md

A terminal tool that streams a USB serial device to the screen and to one log
file per day. Read `README.md` for what works today.

## Verification loop

Run all six before every commit. `.github/workflows/ci.yml` runs the same list;
if the two disagree, CI wins.

```sh
gofmt -l .                     # prints nothing on success, and exits 0 either way
go vet ./...
go tool staticcheck ./...
go test ./...
go test -coverprofile=coverage.out . ./internal/... && go tool cover -func=coverage.out | tail -1
go tool govulncheck ./...
```

Coverage floor is 80%, measured over `.` and `./internal/...` only — `./cmd/...`
is argv wiring and must not drag the ratchet down. Raise the floor only after
the suite has already cleared the new level.

CI-only, deliberately: `go test -race` (needs cgo), `go mod tidy` leaving no
diff, and cross-compilation to linux amd64, arm64, armv7, armv6.

A green gate does not mean the tool works. Exercise a serial change against the
fake device and read the output before calling it done.

## Testing without hardware

`internal/fakedev` allocates a pseudo-terminal whose slave path opens exactly
like `/dev/ttyUSB0`. No cable, no `socat`, no root.

```go
dev, _ := fakedev.Open()  // dev.Path is e.g. /dev/pts/7
dev.Send("A\rB\r\nC\n")   // exactly what the wire carries
dev.Unplug()              // hangs the reader up like pulling the lead
```

`fakedev.Boot` is the real device's power-on transcript, chunked as the device
chunks it. Use it rather than inventing sample output: it is the only sample
containing every awkward case at once. `go run ./cmd/fakedev` is the same rig as
a command, for driving the built binary by hand.

Time is injected, never slept on. `DailyWriter.now` and `stream`'s `now`
argument exist so a rollover test moves the clock instead of waiting for
midnight. Keep that for anything time-dependent.

## Facts about the device that cost time to learn

- **Silence is normal.** Gaps of up to 30 minutes between lines. Nothing may
  treat quiet as end-of-stream.
- **Do not read the port through `bufio.Reader`.** It returns
  `io.ErrNoProgress` after 100 consecutive empty reads — 20 seconds at a 200 ms
  timeout. `go.bug.st/serial` returns `(0, nil)` on timeout, so an idle device
  kills the capture. Read into a byte slice directly.
- **Deassert DTR and RTS when opening.** `go.bug.st/serial` asserts both by
  default, which resets the attached board.
- **Three line endings, all real:** `\r\n`, lone `\n`, lone `\r`. All terminate
  a line; strip the terminator. Flush an unterminated fragment after an idle
  gap rather than holding it forever.
- Lines reach 311 characters. Decoding is lossy UTF-8; byte-exact capture is
  not a goal.
- **A hangup is not `io.EOF`.** `go.bug.st/serial` reports a disconnected
  port as `*PortError{PortClosed}`, because Linux leaves it readable with
  zero-length data. Both that and a plain end of input mean the device is
  gone, never that the session finished.
- **An unplug is routine, not exceptional.** Re-enumeration, USB autosuspend
  and a nudged cable all happen over a run of weeks, and `/dev/ttyUSB0` can
  come back as `ttyUSB1`. Reopening resolves the device again; it never
  reuses the old name.

## Rules

- **Append, never truncate.** A restart on the same day continues that day's
  file. Captured data cannot be recovered by re-running.
- **Nothing on disk until the first line.** `--version` and a bad flag must
  leave no directories behind.
- Roll the file over on write, not on a timer, so an idle logger leaves no
  empty files.
- A cancelled context is a clean stop, not an error. Ctrl-C is the normal way
  to end a session.
- Never commit captured data. `.gitignore` covers `/logs/` and `*.log`.
- **Read failures reconnect; write failures are fatal.** `writeError` exists
  to keep them apart. A full disk or read-only card must stop the capture
  with one error and exit 1, never spin reopening a device whose data has
  nowhere to go.
- **Nothing unbounded per device byte.** The pending line is capped at
  `maxLine` and marked when cut, because a device that never sends a
  terminator would otherwise be held in memory in full. Any new buffer needs
  the same answer: 512 MiB of terminator-free traffic must cost a few MiB.
- **Deleting captured data is opt-in.** `--keep-days` defaults to off, prunes
  only names this prefix could have written, and a failed delete is ignored:
  housekeeping must never be why a capture stops.
- Flush the file on a throttle (`syncEvery`) rather than per line: a Pi that
  loses power must not lose the day's tail, and an SD card must not be worn
  out to prevent it.

## Constraints

- **Linux only.** Raspberry Pis and an x86 Linux dev box. No Windows or macOS
  support, build targets or conditional code, and no `COM3` in an example.
- **Pure Go, `CGO_ENABLED=0`.** The Pi has no Go toolchain and installs a
  released binary with `curl`, so a dependency that will not cross-compile to
  arm cannot ship. `go.bug.st/serial` and its `enumerator` are pure Go on Linux.
- Go alone must build, test and verify this repo: no task runner, wrapper
  script or tool manager as a required step. `staticcheck` and `govulncheck`
  are `go.mod` `tool` directives, so CI needs no install step.
- Do not pass `-ldflags` by hand. `version.go` reads `debug.ReadBuildInfo`, and
  `-X main.version` is set by the release workflow and nowhere else.
- Out of scope on purpose: sending data to the device, config files, log
  retention, multiple devices at once, protocol decoding.
- Commits follow Conventional Commits.

## Releases

Push a `v*` tag; `release.yml` runs the gate, builds the four static binaries
and `checksums.txt`, and publishes them with install instructions. Rehearse any
change to the build steps locally, and start the arm binaries under
`qemu-arm-static` — link-clean is not the same as runnable.

Publishing from the web UI is the same thing arriving in the other order: the
release exists before the job runs. Publish attaches the binaries to it and
rewrites the notes, so either route ends with a release whose download links
resolve. Keep it that way — a release with no assets is worse than no release,
because its own install instructions 404.
