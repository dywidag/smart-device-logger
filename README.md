# smart-device-logger

A terminal tool that streams data from a USB serial device, shows it on screen,
and appends it to one log file per day. Think of a small, scriptable HTerm or
PuTTY that keeps the record for you.

## Status: skeleton

The repository, toolchain, verification loop and the **log path** are done. The
**serial path** is not written yet.

Working today:

| Piece | State |
| --- | --- |
| Daily log files, with midnight rollover | done (`logfile.go`) |
| Per-line timestamps, screen + file at once | done (`stream.go`) |
| Flags, version stamping, Ctrl-C shutdown | done (`main.go`) |
| Serial port discovery and a picker UI | not started |
| Opening a port (baud, data bits, parity) | not started |

So the input is standard input for now, which makes the whole pipeline real and
testable without a device attached:

```sh
go build -o smart-device-logger .
cat /dev/ttyUSB0 | ./smart-device-logger --log-dir ~/device-logs
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

Reading `/dev/ttyUSB0` needs the `dialout` group. Once, then log out and in:

```sh
sudo usermod -aG dialout "$USER"
```

Releases are cut by pushing a tag — `.github/workflows/release.yml` builds and
uploads all four binaries. Nothing is built by hand.

Alternatives, if the machine already has a current Go:
`go install github.com/jonnyasmith/smart-device-logger@latest`, or
cross-compile from the dev box and `scp` the binary over.

## Usage

```
--log-dir     directory to write daily log files into (default "logs")
--log-prefix  leading part of each log file name (default "session")
--version     print the version and exit
```

Files are named `<prefix>-YYYY-MM-DD.log`. Writing continues into an existing
day's file rather than truncating it, so restarting the tool never loses a
session. Nothing is created on disk until the first line arrives.

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

Flat `package main`, one file per module — the same shape as the other Go tools
in this account.

- `main.go` — flags, signal handling, wiring. `run` is the real entry point and
  takes its arguments and streams as parameters, so tests drive it directly.
- `stream.go` — `stream` copies a reader to a writer, one line at a time,
  stamping each line. `record` renders a single line.
- `logfile.go` — `DailyWriter`, an `io.WriteCloser` that opens
  `<dir>/<prefix>-YYYY-MM-DD.log` lazily and rolls over on the first write of a
  new local day. Safe for concurrent use.

## Next pieces

1. **Discovery** — list candidate devices from sysfs, with the USB `VID:PID`,
   manufacturer and product so the list names the device rather than its path.
2. **Picker** — choose from the list when more than one device is present, and
   accept `--port` to skip the prompt.
3. **Open** — baud rate, data bits, parity, stop bits, read timeout, and
   reconnect when the device is unplugged and returns.

Likely dependencies, neither added yet because nothing uses them:

- [`go.bug.st/serial`](https://pkg.go.dev/go.bug.st/serial) — serial port
  access and USB enumeration. Pure Go on Linux, verified for amd64, arm64,
  armv7 and armv6, so `CGO_ENABLED=0` holds and Pi binaries cross-compile.
- [`bubbletea`](https://github.com/charmbracelet/bubbletea) — for the picker,
  matching the other TUIs in this account.

`stream` takes an `io.Reader`, so an open port drops straight in where
`os.Stdin` is today.

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
