# Implementation plan

How `smart-device-logger` gets from the current skeleton to a tool you can hand
someone with a USB device and no instructions.

Each phase names the files it touches, the contract it adds, the tests that
hold it, and a **verification goal** — an observable result with real hardware,
not a green test run. A phase is done when its verification goal is met and the
six-command loop in [AGENTS.md](AGENTS.md) is clean.

## Where we are

Done: daily log files with midnight rollover (`logfile.go`), per-line stamping
to screen and file (`stream.go`), flags and clean Ctrl-C shutdown (`main.go`),
CI with race detector, coverage floor and a cross-build matrix.

Not done: everything to do with a serial port. Input is `os.Stdin`.

## Decisions to make before phase 1

### Library

Use [`go.bug.st/serial`](https://pkg.go.dev/go.bug.st/serial) v1.8.0
(BSD-3-Clause). Verified API:

- `serial.GetPortsList() ([]string, error)` — pure Go, names only.
- `serial.Open(name string, mode *serial.Mode) (Port, error)`.
- `serial.Mode{BaudRate, DataBits, Parity, StopBits, InitialStatusBits}`.
- `Port` is an `io.ReadWriteCloser` and adds `SetReadTimeout(d)`,
  `ResetInputBuffer`, `SetDTR`, `SetRTS`, `GetModemStatusBits`, `Break`.
- `serial.PortError` with `Code() PortErrorCode` (`PortBusy`, and the rest) —
  this is how "the port is already open in another program" is detected rather
  than by matching strings.
- `enumerator.GetDetailedPortsList()` returns `*PortDetails` with `Name`,
  `IsUSB`, `VID`, `PID`, `SerialNumber`, and — with active probing —
  `Manufacturer` and `Product`.

`Port.Read` blocks until at least one byte arrives, so `SetReadTimeout` is what
makes cancellation prompt. That replaces the goroutine race in `run` with a
real wake-up.

### The cgo problem — decide in phase 1, not later

`enumerator` needs the IOKit framework on macOS, so it **requires cgo there**
and cannot be cross-compiled to `darwin/*`. `AGENTS.md` currently requires
`CGO_ENABLED=0` everywhere. Both cannot hold. Three ways out:

| Option | Cost |
| --- | --- |
| **A. Use `enumerator`, build darwin on a `macos-latest` runner with cgo** | CI matrix grows a runner; no cross-compile to darwin |
| **B. Use `GetPortsList` only, pure Go** | Device list is bare paths — `/dev/ttyUSB0`, `COM3` — with no product name to choose by |
| **C. `enumerator` behind a build tag, `GetPortsList` as the fallback** | Two discovery paths to test, and the macOS build is the one that gets less use |

**Recommendation: A.** The whole point of the picker is "which of these is my
device", and `VID:PID Manufacturer Product` is the only thing that answers it.
Take the cgo cost on one platform and update the `CGO_ENABLED=0` rule in
`AGENTS.md` to say "linux and windows", so the documented rule stays true.

### Linux permissions

Reading `/dev/ttyUSB0` needs membership of `dialout` (or `uucp`). This is the
most common first-run failure. The tool must say so by name rather than print
`permission denied`.

---

## Phase 1 — Discovery

**Files:** `device.go`, `device_test.go`. `main.go` gains `--list`.

**Contract**

```go
type Device struct {
    Name         string // /dev/ttyUSB0, COM3
    IsUSB        bool
    VID, PID     string
    SerialNumber string
    Manufacturer string
    Product      string
}

func (d Device) Label() string          // "/dev/ttyUSB0  1a86:7523 QinHeng CH340"
type lister func() ([]Device, error)    // the seam; the real one wraps enumerator
func listDevices() ([]Device, error)
```

Sort USB devices first, then by name, so the thing the user just plugged in is
near the top. `--list` prints the labels and exits 0, or exits 1 with
`no serial devices found` on an empty list.

**Tests** (against an injected `lister`, no hardware): label rendering with and
without USB metadata, ordering, the empty case.

**Verification goal**

With your device unplugged, `smart-device-logger --list` reports none. Plug it
in, run again, and the real device appears with a name you recognise. Paste the
output into the phase-1 commit message. On a machine without `dialout`
membership, the error names the group and the command to fix it.

---

## Phase 2 — Opening a port and streaming from it

**Files:** `port.go`, `port_test.go`. `main.go` gains `--port`, `--baud`,
`--data-bits`, `--parity`, `--stop-bits`, `--read-timeout`.

**Contract**

```go
type PortConfig struct {
    Name     string
    Mode     serial.Mode
    ReadTimeout time.Duration // default 250ms; how fast Ctrl-C answers
}

func openPort(cfg PortConfig) (io.ReadCloser, error)
```

Defaults 115200 8N1. `openPort` maps `serial.PortError` codes to sentences a
user can act on: `PortBusy` → "already open in another program", permission
errors → the `dialout` line from phase 1, missing device → "not found; run
--list".

`stream` must treat a read timeout as "nothing yet", not as end of input — with
a timeout set, `Port.Read` returns `(0, nil)` on expiry, so the loop keeps
going and re-checks the context. **Add this test before the code**: a reader
that returns `(0, nil)` three times then a line must not terminate the stream.
Once the timeout is in place, drop the goroutine race in `run` and let the read
wake up on its own.

**Tests:** flag → `serial.Mode` mapping including every parity and stop-bit
spelling; rejection of `--data-bits 9`; error mapping per `PortErrorCode`;
`(0, nil)` handling in `stream`.

**Verification goal**

`smart-device-logger --port /dev/ttyUSB0 --baud 115200` prints live device
output and writes the same lines to today's file. Ctrl-C returns in under a
second **while the device is silent** — time it. Open the port in another
program first and confirm the busy message. Run at the wrong baud rate and
confirm the tool logs the mojibake rather than crashing.

---

## Phase 3 — Choosing without a UI

**Files:** `select.go`, `select_test.go`.

```go
func selectDevice(devices []Device, want string) (Device, error)
```

No `--port` and exactly one device → use it and say so on stderr. No `--port`
and several → error listing the choices. `--port` given → match the full name
first, then a unique suffix (`ttyUSB0`, `USB0`), so nobody types
`/dev/serial/by-id/usb-...` twice. Ambiguous suffix → error naming the
candidates. This is the whole selection rule, and it must work before any TUI
exists, because it is what scripts and `systemd` units use.

**Tests:** every branch — zero, one, many, exact, suffix, ambiguous, unknown.

**Verification goal**

With one device plugged in, bare `smart-device-logger` starts logging it. With
two, it refuses and lists both. `--port USB0` picks the right one.

---

## Phase 4 — Picker UI

**Files:** `picker.go`, `picker_test.go`. Adds `bubbletea`, `bubbles`,
`lipgloss` — the same stack as the other TUIs in this account.

Shown only when stdin and stdout are terminals **and** no `--port` was given
and more than one device is present. Keys: up/down, `enter` select, `r`
refresh the list, `q`/`esc` quit with exit code 130. `--no-ui` forces the phase
3 behaviour.

**Tests:** drive `model.Update` with keypresses — the convention in
`crt-maintain-user-cli`. Cursor movement at both ends of the list, selection,
refresh replacing the list, quit.

**Verification goal**

Run with two devices attached and pick the second one. Unplug it while the
picker is open, press `r`, and watch it leave the list. Piping the binary's
output to a file must skip the picker entirely rather than hang.

---

## Phase 5 — The live view

**Files:** `view.go`. `main.go` grows a status line.

A status line under the stream: device label, baud, uptime, lines and bytes
logged, and the file currently being written. Refresh it at most a few times a
second — a device at 115200 can produce lines faster than a terminal can scroll
and the status line must not become the bottleneck.

Decide here, and write it down: **plain scrolling output plus a status line**,
not a full-screen viewport. Scrollback, `grep`, and copy-paste all keep working
that way, which is most of why people reach for a terminal logger.

**Tests:** counters, and the throttle (injected clock, as with everything
time-dependent).

**Verification goal**

Run against a device emitting 1000 lines/second for a minute. The line count in
the status matches `wc -l` on the log file, no lines are dropped, and the
terminal stays responsive.

---

## Phase 6 — Survive an unplug

**Files:** `reconnect.go`, `reconnect_test.go`.

An unplugged USB adapter makes reads fail permanently. Wrap the stream in a
retry with a capped backoff (say 250 ms doubling to 5 s) that reopens by
**serial number** where one exists, because `/dev/ttyUSB0` can come back as
`ttyUSB1`. Write markers into the log so the record explains its own gap:

```
2026-03-04T09:14:02.113+00:00 --- device disconnected ---
2026-03-04T09:14:07.480+00:00 --- reconnected to /dev/ttyUSB1 ---
```

`--reconnect=false` restores fail-fast for scripts. Rollover and reconnect are
independent: reconnecting mid-day continues the same file.

**Tests:** a fake opener that fails N times then succeeds — markers written,
backoff bounded, context cancellation honoured mid-backoff.

**Verification goal**

Start logging, physically pull the USB device, wait ten seconds, plug it back
in. The tool reconnects on its own and the log contains both markers with the
real gap between them.

---

## Phase 7 — Fit for long runs

**Files:** across the existing ones.

- **Binary-safe.** A device emitting non-UTF-8 bytes must not corrupt the file
  or the terminal. Log bytes verbatim to the file; escape control characters on
  screen only.
- **Cap the line length** (say 64 KiB) so a device stuck without a newline
  cannot exhaust memory. Flush the partial line with a marker.
- **Disk full** must produce one clear error and a non-zero exit, not a silent
  truncated log.
- **`--keep-days N`** deletes daily files older than N days at each rollover.
  Off by default: deleting captured data must be asked for.

**Tests:** invalid UTF-8 round-trips byte-for-byte; the length cap; retention
deletes only files matching this prefix's pattern and only beyond the window.

**Verification goal**

Leave the tool running against a chatty device overnight. Next morning: two
files, the rollover happened at local midnight, no lines lost at the boundary,
memory flat in `ps`.

---

## Phase 8 — Release

**Files:** `.goreleaser.yaml`, `.github/workflows/release.yml`.

Tag `v*` builds windows/linux/darwin × amd64/arm64 and stamps `main.version`
via ldflags. Darwin needs cgo per the phase-1 decision, so it builds on a macOS
runner.

**Verification goal**

Download the artefact for a machine you have not built on, run `--version`, and
log a real device with it.

---

## The standing gate

Every phase, before commit:

```sh
gofmt -l .                     # must print nothing
go vet ./...
go tool staticcheck ./...
go test ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1
go tool govulncheck ./...
```

Plus, per phase:

- The verification goal above, done on real hardware, with the result written
  into the commit message. A phase that only has passing tests is not done.
- Coverage floor raised only after the suite already clears the new level.
- `README.md`'s status table updated in the same commit — it is the thing that
  tells the next person what is real.

## Acceptance for v1.0

Someone who has never seen the tool can:

1. Plug in a USB device.
2. Run `smart-device-logger`, pick their device from a list that names it.
3. Watch the data on screen.
4. Find today's log in a predictable place, with timestamps, still being
   appended to.
5. Leave it running for a week and get seven files, with any unplug and replug
   marked in them.

Each of those five is a manual test to run before tagging, on Linux and on
Windows.

## Risks

| Risk | Handling |
| --- | --- |
| cgo on macOS conflicts with the pure-Go rule | Decide in phase 1, then correct `AGENTS.md` |
| No hardware for CI | Every seam takes an injected reader or lister; hardware checks stay manual and are recorded in commit messages |
| Windows behaves differently — COM naming, no `dialout` | Run the phase 2, 3 and 4 verification goals on Windows too, not only Linux |
| High-rate devices outrun the terminal | Phase 5 throttles the status line and phase 5's goal measures it |
| Silent data loss on a full disk | Phase 7 makes it a loud failure |
