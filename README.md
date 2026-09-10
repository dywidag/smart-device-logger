# smart-device-logger

A terminal tool that streams data from a USB serial device, shows it on screen,
and appends it to one log file per day. Think of a small, scriptable HTerm or
PuTTY that keeps the record for you.

## Status

The tool works end to end: it opens the device, streams every line to the
screen and to the day's log file, and keeps a small status block pinned under
the stream while it runs.

| Piece | State |
| --- | --- |
| Daily log files, with midnight rollover | done (`logfile.go`) |
| Per-line timestamps, screen + file at once | done (`stream.go`, `line.go`) |
| Flags, version stamping, Ctrl-C shutdown | done (`main.go`) |
| Serial port discovery and opening | done (`device.go`) |
| Status block on the terminal | done (`status.go`) |
| Surviving an unplug, with markers in the log | done (`reconnect.go`) |
| Fit for long runs: line cap, flushes, retention | done (`line.go`, `logfile.go`) |

```sh
go build -o smart-device-logger .
./smart-device-logger --port /dev/ttyUSB0
./smart-device-logger                     # with exactly one device plugged in
```

## Install on a Raspberry Pi

**No Go toolchain on the Pi.** Releases ship one static binary per Pi
generation; the install is a download and a copy.

Check which one you need — `uname -m` prints `aarch64` for arm64, `armv7l` for
armv7, `armv6l` for armv6:

```sh
curl -fsSL -o smart-device-logger \
  https://github.com/jonnyasmith/smart-device-logger/releases/latest/download/smart-device-logger-linux-arm64
sudo install -m 0755 smart-device-logger /usr/local/bin/
smart-device-logger --version
```

`checksums.txt` is attached to every release: `sha256sum -c checksums.txt`.

Reading `/dev/ttyUSB0` needs membership of the group that owns the device
file — `dialout` on Raspberry Pi OS, `uucp` on Arch. The tool reads the group
off the device and prints the exact command when the open is refused. Once,
then log out and in:

```sh
sudo usermod -aG dialout "$USER"
```

Releases are cut by pushing a tag — `.github/workflows/release.yml` builds and
uploads all four binaries. Nothing is built by hand.

Alternatives, if the machine already has a current Go:
`go install github.com/jonnyasmith/smart-device-logger@latest`, or
cross-compile from the dev box and `scp` the binary over.

## Running over SSH

The tool stops when its terminal goes away, so a capture started over SSH ends
when the connection does. Run it inside `tmux`, which keeps the terminal alive
on the Pi whether anyone is connected or not.

Once, if the Pi has no `tmux`:

```sh
sudo apt install tmux
```

Start a capture:

```sh
ssh pi@raspberrypi
tmux new -s log          # opens a shell inside a session named "log"
smart-device-logger      # start the capture in that shell
```

Then, in order:

| To | Do |
| --- | --- |
| Leave it running | `Ctrl-B` then `D` — detaches; the SSH connection can be closed |
| Come back to it | `ssh pi@raspberrypi`, then `tmux attach -t log` |
| See what is running | `tmux ls` |
| Stop the capture | attach, then `Ctrl-C` — the clean stop, exit 0 |
| Close the session too | `exit` in the session's shell, or `tmux kill-session -t log` |

Attaching restores the stream and the status block as they were; the app never
knows it was detached.

Two details. `Ctrl-B` is tmux's own prefix key and the only keystroke the app
does not receive — `Ctrl-C` still reaches it normally. And start the app inside
the session rather than as `tmux new -s log 'smart-device-logger'`: with the
command baked into the `new` line, the session disappears the moment the app
exits and takes the reason with it.

`tmux` does not survive a reboot. Logging from boot unattended wants a systemd
unit, which is deliberately not part of this tool yet.

## Usage

```
--port          serial device: a path, a bare name such as ttyUSB0, or a
                /dev/serial/by-id entry (default: the only device present)
--log-dir       directory to write daily log files into
                (default ~/.local/state/smart-device-logger)
--log-prefix    leading part of each log file name (default "session")
--keep-days     delete daily files older than this many days at each
                rollover (default 0: keep everything)
--read-timeout  how long a read waits for data before checking for Ctrl-C (default 200ms)
--reconnect     reopen the device after an unplug instead of exiting (default true)
--version       print the version and exit
```

The port opens at 115200 8N1 with DTR and RTS dropped straight after the
open, so a board that resets on DTR is not rebooted. With no `--port`, the
one device present is used; with several, the tool exits 1 and lists them so
the right one can be passed. Bad usage exits 2, Ctrl-C exits 0.

Every line goes to stdout and to the day's file with an ISO-8601 stamp
(`2026-09-10T14:00:01.000+01:00 ...`). `\r\n`, lone `\n` and lone `\r` all end
a line; a fragment with no terminator is written after 200 ms of quiet.
Invalid UTF-8 becomes U+FFFD. A two-line status block draws on stderr when
that is a terminal, so `> capture.txt` gets data lines only, and a redirected
stderr gets no escape sequences.

A line is cut at 64 KiB and marked
(`... --- cut at 65536 bytes, line continues ---`) so a device stuck without
a terminator — the wrong baud rate, wedged firmware, noise on the lead —
cannot grow the process until the kernel kills it.

Files are named `<prefix>-YYYY-MM-DD.log` after the local date. Writing
continues into an existing day's file rather than truncating it, so restarting
the tool never loses a session. Nothing is created on disk until the first
line arrives. The file is flushed to disk at most every 5 seconds and at
every rollover, so a power cut costs seconds of record rather than the day's
tail. `--keep-days 14` deletes older files at each rollover, matching only
this prefix's names; without it nothing is ever deleted.

## Long runs and unplugs

A USB serial device disappears from time to time — a re-enumeration, an
autosuspend blip, a nudged cable — and by default the tool waits for it to
come back rather than exiting. The gap is explained in the log itself:

```
2026-09-10T17:52:20.565Z --- device disconnected: /dev/ttyUSB0 (read device: Port has been closed) ---
2026-09-10T17:52:22.326Z --- reconnected to /dev/ttyUSB1 ---
```

Retries back off from 250 ms to 5 s and continue for as long as the device is
away. Reopening resolves the device again rather than reusing the old name,
so an adapter that comes back as `ttyUSB1` is picked up, and the status block
renames itself to match. `--reconnect=false` restores fail-fast for scripts:
an unplug then exits 1.

A failure to *write* is never retried. A full disk, a read-only SD card or a
failed flush stops the capture with one clear error and exit 1, because
reopening a device whose data has nowhere to go only loses it faster.

Measured, not assumed: 480 MiB of traffic across 60 simulated days and 60
unplugs leaves heap, goroutines and descriptors where they started, and
512 MiB with no terminator in it costs a bounded few MiB.

## Verifying without the hardware

`cmd/fakedev` is a serial device with no serial device: it allocates a
pseudo-terminal and hands back a real character device path that opens,
configures and reads exactly like `/dev/ttyUSB0`. This is the verification
loop — no FTDI lead, no `socat`, no root.

```sh
go run ./cmd/fakedev            # prints e.g. /dev/pts/7, then replays the real boot transcript
go run ./cmd/fakedev -script endings    # A\rB\r\nC\n
go run ./cmd/fakedev -script fragment   # MEMS........ with no terminator
go run ./cmd/fakedev -script silence    # opens and says nothing
go run ./cmd/fakedev -script garbage    # invalid UTF-8
go run ./cmd/fakedev -script boot -unplug 10s
```

The transcript in `internal/fakedev/transcript.go` is the real VoidCellular
v1-4-p2 boot, taken from the web logger's fixture. It reproduces every case
the device actually produces: CRLF, a bare LF on the CSV header, lone CR in
the AT exchange, an unterminated `MEMS........` fragment, a line whose
terminator arrives in the next read, and a 311-character `+COPS` answer.

`internal/fakedev` is also usable directly from a test — `fakedev.Open()`
gives a `Device` you write to and `Unplug()` to hang the reader up.

## Layout

`package main` at the root, one file per module — the same shape as the other
Go tools in this account. `internal/` and `cmd/` hold test scaffolding only.

- `main.go` — flags, signal handling, wiring. `run` is the real entry point and
  takes its arguments and streams as parameters, so tests drive it directly.
- `device.go` — discovery from `/dev/serial/by-id` and the library enumerator,
  `--port` resolution, opening at 115200 8N1 with DTR/RTS dropped, and the
  permission-denied message.
- `stream.go` — `stream` copies a reader to a writer, one line at a time,
  reading into a byte slice so a silent device is never end-of-stream, and
  `writeError`, which marks the failures that must stop a capture.
- `reconnect.go` — `capture`, the loop that reopens the device after an
  unplug, with the backoff and the log markers.
- `line.go` — the line splitter, idle flush, 64 KiB cap, timestamp layout and
  `record`.
- `logfile.go` — `DailyWriter`, an `io.WriteCloser` that opens
  `<dir>/<prefix>-YYYY-MM-DD.log` lazily, rolls over on the first write of a
  new local day, flushes on a throttle and prunes to `--keep-days`. Safe for
  concurrent use.
- `status.go` — the status block on stderr.
- `version.go` — what `--version` reports, from the release ldflags stamp or
  `debug.ReadBuildInfo`.
- `internal/fakedev`, `cmd/fakedev` — the fake serial device above.

## Where the standard lives

`.wayfinder/serial-logger/ANSWER-KEY.md` is what the finished tool is judged
against — 26 binary checks, an out-of-scope list, and three Unknowns nobody
has decided. `MAP.md` beside it holds the reasoning for each check and the
measured facts behind them. `PLAN.md` predates both; where they disagree, the
map wins.

## Development

**Target: Linux only** — Raspberry Pis with the device on USB, and an x86
Linux dev box to build from. No Windows, no macOS.

Go is the only requirement. The version comes from the `go` directive in
`go.mod`, and `GOTOOLCHAIN=auto` fetches it. `staticcheck` and `govulncheck` are
`tool` directives, so no install step is needed.

Build for a Pi from the dev box:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o smart-device-logger .   # Pi 4/5, 64-bit
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o smart-device-logger .  # 32-bit Pi OS
```

Run all six before every commit:

```sh
gofmt -l .                     # must print nothing
go vet ./...
go tool staticcheck ./...
go test ./...
go test -coverprofile=coverage.out . ./internal/... && go tool cover -func=coverage.out | tail -1
go tool govulncheck ./...
```

`.github/workflows/ci.yml` is the source of truth for that list, and adds the
race detector, `go mod tidy` cleanliness, a coverage floor, and a build of
every Linux target (amd64, arm64, armv7, armv6). See [AGENTS.md](AGENTS.md)
for the conventions, and [PLAN.md](PLAN.md) for the route to v1.0.
