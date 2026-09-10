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

## Usage

```
--log-dir     directory to write daily log files into (default "logs")
--log-prefix  leading part of each log file name (default "session")
--version     print the version and exit
```

Files are named `<prefix>-YYYY-MM-DD.log`. Writing continues into an existing
day's file rather than truncating it, so restarting the tool never loses a
session. Nothing is created on disk until the first line arrives.

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
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1
go tool govulncheck ./...
```

`.github/workflows/ci.yml` is the source of truth for that list, and adds the
race detector, `go mod tidy` cleanliness, a coverage floor, and a build of
every Linux target (amd64, arm64, armv7, armv6). See [AGENTS.md](AGENTS.md)
for the conventions, and [PLAN.md](PLAN.md) for the route to v1.0.
