# MAP — smart-device-logger

## Destination

A terminal tool for Linux and Raspberry Pi: pick the plugged-in USB serial
device, watch its output live, and get a timestamped daily log file that is
still readable and complete a week later.

## Open questions

<!-- ordered. This list is the running order — re-sort it when new questions arrive. -->

1. [x] What the device transmits: baud, framing, line endings, burst size — research (read from the existing web logger)
2. [ ] How does the tool tell an unplug from a deliberate close, and what does it do next — research; **unresolved, became U1**. The hardware spike ran all session and the USB plug was never removed, so the behaviour was never observed. Source reading got as far as: the kernel makes it a race between a zero-byte read and `EIO`, the library maps zero to `PortClosed`, and `PortClosed` is also what a deliberate `Close` produces. Whether reconnect should be automatic was never decided either.
3. [x] How would you know the UI shape came out wrong — grilling
4. [x] How would you know the log location came out wrong — grilling
5. [x] How would you know the silence behaviour came out wrong — grilling
6. [x] What does the status block show, and what happens when stdout is piped — prototype
7. [ ] What does `--port` accept, and what happens with two devices attached — grilling
8. [ ] Is the log text-only, or must raw bytes survive a device that emits non-UTF-8 — grilling
9. [ ] What exit code does each ending produce — grilling
10. [ ] What does the tool say when it cannot open the port for permissions — grilling
11. [ ] Does a week-long capture need retention or size caps — grilling

## Not yet specified

<!-- the fog: in-scope, but you can't phrase the question sharply yet -->

- How a capture is left running across an SSH disconnect. Not a service (that
  is out of scope), but "started it, closed the laptop" is a real way this
  gets used and it is unclear what should happen.

## Out of scope

<!-- ruled beyond the destination. Adding these makes the result worse. -->

- Sending data to the device (a TX line, an input box). Turns a logger into a
  terminal emulator and doubles the UI.
- Logging more than one device at once. Turns one stream into a scheduler.
- Running as a boot service or systemd unit. `--port X >> file` under systemd
  already covers it if it is ever wanted.
- A config file. Flags only.
- A web UI, log upload, or log compression.
- Protocol decoding — Modbus, NMEA, JSON parsing. The tool logs bytes and
  timestamps; interpretation is someone else's job.

## Answers

### What is the finished thing — interactive tool or unattended service

**Answer:** An interactive terminal tool. You run it, watch it, stop it. Logs
are the record it leaves behind, not a service's output.

**Why:** The user's first description was "a bit like HTerm", which is a thing
you sit in front of. The draft plan straddled both and contradicted itself: a
device picker and a live view only make sense with a person present, while
"leave it running for a week" implies a unit file and no UI. Choosing one kills
the contradiction, and the service case is already reachable with
`--port X >> file` under systemd if it is ever wanted.

**Check:** Running it prints device output to stdout and returns to the shell
prompt on Ctrl-C, with no boot-time or service configuration required.
**Judged by:** run it
**Reference:** —

### How much UI

**Answer:** A full-screen picker with help text when there is a device to
choose; plain scrolling output once logging starts.

**Why:** The user's instinct was that a TUI can carry helper text explaining
how to use the tool. That is true exactly where there is a choice to make — the
picker. Once bytes are flowing there is nothing to explain, and a full-screen
viewport would cost terminal scrollback, `grep`, copy-paste, and piping, which
is most of why a person reaches for a terminal logger.

**Check:** `smart-device-logger --port /dev/ttyUSB0 | grep ERROR` prints only
matching lines, and terminal scrollback still shows output from before the
tool started.
**Judged by:** run it
**Reference:** —

### Where logs go by default

**Answer:** `~/.local/state/smart-device-logger/`, overridable with
`--log-dir`.

**Why:** The default was `./logs`, relative to wherever the shell happened to
be. A logger whose output location depends on the current directory will lose a
capture eventually. XDG's state directory is the standard place for exactly
this kind of file.

**Check:** Run once from `/tmp` and once from `$HOME` with no `--log-dir`;
both append to the same file under `~/.local/state/smart-device-logger/`.
**Judged by:** run it
**Reference:** —

### What happens when the device is silent for a long time

**Answer:** Wait indefinitely. The real device can go up to 30 minutes
between transmissions, so no silence, of any length, may end a capture.

**Why:** Silence is the normal state of this device, not an error — the user
states gaps of up to 30 minutes. A logger that treats quiet as end-of-stream
cannot capture it at all. This also rules out the current implementation:
`bufio.Reader` aborts after 100 empty reads, which at a 200 ms read timeout
is 20 seconds — 90 times shorter than a normal gap.

**Check:** With the port open and no data for 35 minutes, the process is
still running and logs the next line it receives.
**Judged by:** run it
**Reference:** —

### What the status block shows

**Answer:** Prototype variant 2 — a two-line block: port, adapter and line
settings on the first line; time since the last line, line count, byte count
and the full log path on the second. Redrawn in place at least once a second.

**Why:** Picked by the user from three rough variants after seeing all three
play out a 29-minute silence. Variant 1 carried the same live counter with
less context; variant 3 dropped the counter entirely. The reason for the pick
was not stated beyond "proto 2 is the best" — the visible difference is that
2 is the only one showing the port settings and the full log path, so a
capture can be found and its settings confirmed without leaving the screen.

**Check:** Side by side with variant 2 in
`.wayfinder/serial-logger/prototypes/status-line.html`, judged blind.
**Judged by:** A/B pick
**Reference:** `.wayfinder/serial-logger/prototypes/status-line.html`

### What happens to the status block when stdout is piped

**Answer:** The status block goes to stderr, always. Piping stdout leaves a
clean stream of timestamped data lines; the block still draws on the
terminal. If stderr is also redirected, nothing is drawn.

**Why:** Variant 2 redraws with ANSI cursor moves (`\x1b[2A`, `\x1b[2K`),
which would corrupt a pipe and destroy scrollback — directly contradicting
the check on the UI-shape decision. stderr resolves it with one
implementation instead of two output modes, and the tool already writes its
startup line there.

**Check:** With stdout piped to `grep`, the piped stream contains no escape
sequences and only timestamped data lines.
**Judged by:** run it
**Reference:** —

### DTR and RTS on open

**Answer:** Deassert both when opening the port, matching the web logger.

**Why:** The web tool for this same device deasserts both immediately after
open (`web-serial.ts:99-103`), while `go.bug.st/serial` asserts both by
default. On a board that auto-resets on DTR that difference reboots the
device every time logging starts — the capture would begin by destroying the
state being captured. The transcript makes the symptom unmistakable: a reset
announces itself with `Bootloader activated` / `VoidCellular v1-4-p2`.

**Check:** Start the tool against a device that has already booted; the log
contains no `Bootloader activated` line.
**Judged by:** run it
**Reference:** —

### What counts as a line ending

**Answer:** `\r\n`, a lone `\n`, and a lone `\r` all terminate a line. A
trailing `\r` at the end of a read is held until the next read decides
whether it was CRLF.

**Why:** The device mixes them — debug and boot lines are CRLF, but the
`mreport` CSV header `bin,count,min,max,avg` arrives with a bare LF
(`monitor/transcripts/boot-and-modem.ts:6-15`). Splitting on `\n` alone, as
the current Go code does, would merge or mangle these.

**Check:** Feeding `A\rB\r\nC\n` produces exactly three log lines: A, B, C.
**Judged by:** run it
**Reference:** —

### Output with no line ending at all

**Answer:** Flush a partial line to screen and file after 200 ms of idle,
matching the web logger's idle flush.

**Why:** The device emits unterminated fragments during selftest —
`MEMS........` sits with no terminator until the result arrives
(`boot-and-modem.ts:18-39`). Without an idle flush, the operator watches a
blank screen during selftest and the log misreports when each step happened.

**Check:** Sending `MEMS........` with no line ending makes it appear within
one second.
**Judged by:** run it
**Reference:** —

### Timestamp format and granularity

**Answer:** Keep per-line stamps in ISO-8601 with a UTC offset
(`2026-09-10T14:00:01.000+01:00`), rather than the web tool's per-chunk
`yyyy-MM-dd HH:mm:ss.SSS`.

**Why:** The web tool takes one clock reading per USB chunk and applies it to
every line in it (`line-splitter.ts:46-51`), so a 47-line boot burst can
share timestamps. Per-line is more accurate and costs nothing. The offset
matters for an overnight capture crossing a DST change, which the web format
cannot express. Parity with existing exports was offered and declined.

**Check:** In a capture spanning the local DST change, the two lines either
side of the change carry different UTC offsets and remain in ascending time
order.
**Judged by:** run it
**Reference:** —

### Non-UTF-8 bytes

**Answer:** Decode lossily to UTF-8; invalid bytes become the replacement
character. Byte-exact capture is out of scope.

**Why:** Matches the web tool, which decodes with
`TextDecoder("utf-8", { fatal: false })` and has a test pinning `ff fe` to
replacement characters (`line-splitter.ts:30`;
`line-splitter.test.ts:101-106`). The device is an AT-command cellular module
emitting ASCII; the whole reference transcript is 864 bytes of printable text
plus CR and LF. A parallel raw-byte file would double the write path for a
case that has not occurred in the tool this one replaces.

**Check:** Feeding the bytes `ff fe` writes a line containing replacement
characters and the process keeps running.
**Judged by:** run it
**Reference:** —

### Choosing a device without the picker

**Answer:** Exactly one device attached and no `--port` — use it, and name it
on stderr. Several attached and no `--port` — refuse and list them. `--port`
accepts a full path, a bare name (`ttyUSB0`), or a `/dev/serial/by-id/` path.

**Why:** The single-device case is the common one and prompting for it is
friction. Refusing when ambiguous is safer than picking: logging the wrong
device looks like a working capture until someone reads it. The `by-id` form
matters because `/dev/ttyUSBn` numbering is not stable across a replug, so it
is the only stable way to name a device in a script.

**Check:** With one device attached and no `--port`, the tool logs it; with
two attached, it exits non-zero and names both.
**Judged by:** run it
**Reference:** —

### Exit codes

**Answer:** 0 for Ctrl-C, 1 for any error, 2 for bad usage.

**Why:** A script needs to tell "the operator stopped it" from "it broke",
and bad flags from runtime failures. Anything finer would be guessing at
scripts nobody has written yet.

**Check:** Ctrl-C during logging exits 0; `--port /dev/nope` exits 1;
`--nonsense` exits 2.
**Judged by:** run it
**Reference:** —

### The permission-denied message

**Answer:** Name the group that owns the device file and give the exact
command, reading the group from the file rather than hardcoding it.

**Why:** This is the most likely first-run failure on a Pi, and `permission
denied` alone tells the user nothing. The group differs by distribution —
the Pi uses `dialout`, the Arch dev box in this project uses `uucp` — so a
hardcoded name would be wrong half the time.

**Check:** As a user not in the port's group, the error names that group and
gives the `usermod -aG` command for it.
**Judged by:** run it
**Reference:** —

### Retention

**Answer:** None. One file per day, kept indefinitely. No `--keep-days`, no
size cap, no compression.

**Why:** A week of this device is a few megabytes — its entire boot burst is
861 bytes. Automatic deletion of captured data can only ever lose something
that cannot be recovered.

**Check:** none — this decision is to build nothing, so it puts nothing on
the bar. Its enforcement is the out-of-scope list.
**Judged by:** —
**Reference:** —

## Facts established

Not decisions — measured or read from primary sources, and load-bearing for
the questions above.

- The attached device is an FTDI TTL-232R lead: `VID=0403 PID=6001
  serial=FTFMFR3N manufacturer=FTDI product=TTL232R`, at `/dev/ttyUSB0`, with
  `/dev/serial/by-id/usb-FTDI_TTL232R_FTFMFR3N-if00-port0`. It is a bare cable
  and transmits nothing by itself: 5 s at 115200 and 5 s at 9600 both gave 0
  bytes and 25 empty reads.
- `Port.Read` returns `(0, nil)` when a read timeout expires with no data
  (`serial_unix.go:91-93`), confirmed against the real cable.
- `bufio.Reader` aborts with `io.ErrNoProgress` after 100 consecutive empty
  reads — measured. At a 200 ms timeout that is 20 s of device silence, and
  the current `stream.go` is built on `bufio`.
- On unplug the kernel hangs the tty up (`usb-serial.c:1190` →
  `tty_port_hangup` sets `TTY_IO_ERROR`). A read then returns either 0 or
  `-EIO` depending on a race with the file-op swap. The library maps 0 to
  `PortError{PortClosed}` (`serial_unix.go:102-103`) and passes `EIO` through
  raw with no `PortErrorCode` (`:105-108`). `PortClosed` is also what a
  deliberate `Close` produces. It never blocks forever.
- `Manufacturer` and `Product` need no active USB probing on Linux; the
  implementation ignores the probe argument and reads sysfs
  (`enumerator/usb_linux.go:20-26`).
- `/dev/ttyUSBn` numbering follows discovery order and is not stable across a
  replug, even in the same physical port. `/dev/serial/by-id/` follows the USB
  serial number; `by-path` follows the physical port
  (`/usr/lib/udev/rules.d/60-serial.rules:11-26`).

From the existing web logger for this same device
(`~/dev/II-User-Interface/src/lib/components/domain/smart-devices/`):

- Port settings are `115200 8N1`, `flowControl: "none"`, read buffer 16384
  (`monitor/transport/web-serial.ts:5,91-98`; `monitor/types.ts:29-42`).
- Immediately after opening it **deasserts DTR and RTS**
  (`web-serial.ts:99-103`). `go.bug.st/serial` asserts both by default.
- Three line terminators are accepted — `\r\n`, lone `\n`, lone `\r` — and a
  chunk-final `\r` is held pending to tell CRLF from a bare CR
  (`monitor/line-splitter.ts:18-21,74-89`).
- A partial line with no terminator is emitted after 200 ms of idle, tagged
  `flush` (`line-splitter.ts:3,51-71`).
- No maximum line length exists anywhere in the implementation
  (`line-splitter.ts:84-90`).
- Timestamps are per **chunk**, not per line: one `now()` per push, applied to
  every line emitted from it (`line-splitter.ts:46-51,104-106`). Screen format
  is `HH:mm:ss.SSS`; the export format is `yyyy-MM-dd HH:mm:ss.SSS`, written
  tab-separated (`components/log-timestamp.ts:6-20`;
  `monitor/components/MonitorLog.svelte:71-78`).
- Bytes are decoded with `TextDecoder("utf-8", { fatal: false })`, so invalid
  bytes become replacement characters and are not recoverable
  (`line-splitter.ts:30,46-51`).
- On unplug it does not retry by default: it emits `disconnected`, toasts
  "Serial device unplugged", preserves the log, and waits for a manual
  Connect unless `autoReconnect` is on
  (`monitor/transport/web-serial.ts:133-171`;
  `routes/admin/smart-devices/+layout.svelte:72-90`).

What the device actually is and sends
(`monitor/transcripts/boot-and-modem.ts`, `monitor/at-commands.ts`,
`command/quick-commands.ts`):

- A **VoidCellular v1-4-p2** board with a **u-blox SARA-R422** cellular
  module (LTE-M / NB-IoT / 2G), an RV3129 RTC, AT45 flash, and a MEMS tilt
  sensor (`boot-and-modem.ts:18-27`; `at-commands.ts:1-3,31-74`).
- It speaks **unprompted on connect**: the mock replays the boot transcript
  with no write first (`monitor/transport/mock.ts:24-26`).
- The whole boot-and-modem transcript is **861 bytes in 12 chunks, producing
  47 log lines** — 46 terminated, 1 from the idle flush. Throughput is a
  non-issue; the earlier "1000 lines/second" figure was invented and is
  discarded.
- Line lengths: ordinary lines 2–27 characters; the longest observed is a
  `+COPS` answer at **311 characters** excluding its CRLF. No line cap is
  needed, and none exists in the web tool.
- All three endings occur: CRLF for ordinary boot/debug lines, a bare LF for
  the CSV header (`bin,count,min,max,avg`), and lone CR inside the AT
  exchange soup, including leading and embedded CRs. Unterminated fragments
  appear during selftest (`MEMS........`)
  (`boot-and-modem.ts:6-15,18-39`; `line-splitter.ts:18-21`).
- `:tiltmon` streams continuously until any byte is received — the one
  command that produces sustained output (`quick-commands.ts:30-35`). Sending
  it is out of scope for this tool, but its output may be seen if someone
  starts it from the web tool first.
