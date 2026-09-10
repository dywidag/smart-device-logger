# ANSWER KEY — smart-device-logger

## How to use this document

You are judging finished work against this standard. Read these rules before you judge anything.

1. Judge **only** the checks in "The bar" below. Do not judge anything else, however obviously good or bad it looks.
2. Every check is **binary** — it passes or it fails. Never give a score, a rating, or a percentage. There is no partial credit.
3. Do not invent a standard. If something matters and isn't on this list, that is deliberate — it is either out of scope or undecided, both of which are listed below.
4. **Items under "Unknown" may not be judged.** They are numbered `U1`, `U2`, and so on. If the work touches one, report `U<number>: CANNOT JUDGE` and stop on that item. Do not guess, do not infer what was probably intended, do not pass it because it looks reasonable. Reporting that you cannot judge something is a correct and expected outcome, not a failure.
5. Items under "Out of scope" must not be rewarded. Work that adds them is **worse**, not better, no matter how impressive it looks.
6. If a check looks arbitrary, open `MAP.md` in this same folder. It holds the reasoning behind every check, linked from the last column. Read the reasoning before deciding a check is wrong.
7. If you built any of this work yourself, stop and say so. Judging your own output is not judging.

### Report your verdict exactly like this

One line per bar check, in order, then one line for each Unknown item the work touched. Nothing else:

```
1: PASS
2: FAIL — <what was wrong, in one line>
3: PASS
U2: CANNOT JUDGE
```

`PASS`, `FAIL` and `CANNOT JUDGE` are the only three verdicts. Bar checks are numbered plain (`1`, `2`); Unknown items carry their `U` (`U1`, `U2`). A bar check is never `CANNOT JUDGE` — every one of them was written to be gradeable, so if you can't grade one, say which and why in a `FAIL` line rather than inventing a verdict.

Then one final line:

```
RESULT: PASS                  — every bar check passed and no Unknown was touched
RESULT: FAIL                  — any bar check failed
RESULT: BLOCKED — U2, U5      — no bar check failed, but the work touched these Unknowns
```

`BLOCKED` means the work cannot be signed off until a person decides the listed items. It is **not** a failure of the work, and it is **not** something you can resolve by looking harder — looking harder is exactly what produces an invented answer. If you are running in a loop, `BLOCKED` ends the loop and hands back to a human; it does not mean try again.

## Destination

A terminal tool for Linux and Raspberry Pi: pick the plugged-in USB serial device, watch its output live, and get a timestamped daily log file that is still readable and complete a week later.

## Setting up to judge this

Two checks need a real device; the rest can be graded with a virtual serial port, which is how they were designed to be graded.

```sh
socat -d -d pty,raw,echo=0,link=/tmp/ttyFAKE pty,raw,echo=0,link=/tmp/ttyFAKE-host &
printf 'hello\r\n' > /tmp/ttyFAKE-host      # transmit to the tool reading /tmp/ttyFAKE
```

Check 10 needs the FTDI device itself. Checks needing two devices (16) can use two `socat` pairs.

## The bar

| # | check | judged by | reference | from decision |
|---|-------|-----------|-----------|---------------|
| 1 | A single command with a `--port` argument starts logging: no config file, no unit file, no boot-time setup exists or is required | run it | — | [interactive tool](MAP.md#what-is-the-finished-thing--interactive-tool-or-unattended-service) |
| 2 | The bytes reaching a pipe on stdout contain no ESC (0x1b) characters | run it | — | [status block when piped](MAP.md#what-happens-to-the-status-block-when-stdout-is-piped) |
| 3 | With stdout piped, the status block still draws on the terminal | run it | — | [status block when piped](MAP.md#what-happens-to-the-status-block-when-stdout-is-piped) |
| 4 | Text printed to the terminal before the tool started is still visible by scrolling up while the tool runs | run it | — | [how much UI](MAP.md#how-much-ui) |
| 5 | Run once from `/tmp` and once from `$HOME` with no `--log-dir`; both append to one file under `~/.local/state/smart-device-logger/` | run it | — | [where logs go](MAP.md#where-logs-go-by-default) |
| 6 | With the port open and no data for 35 minutes, the process is still running | run it | — | [long silence](MAP.md#what-happens-when-the-device-is-silent-for-a-long-time) |
| 7 | After those 35 silent minutes, the next line the device sends is written to the log | run it | — | [long silence](MAP.md#what-happens-when-the-device-is-silent-for-a-long-time) |
| 8 | With `--read-timeout 50ms` and no data for 60 seconds, the process is still running | run it | — | [long silence](MAP.md#what-happens-when-the-device-is-silent-for-a-long-time) |
| 9 | The status block, shown blind beside the reference, is picked as the match | A/B pick | `prototypes/status-line.html` variant 2 | [status block](MAP.md#what-the-status-block-shows) |
| 10 | Starting the tool against an already-booted VoidCellular device logs no `Bootloader activated` line | run it | — | [DTR and RTS](MAP.md#dtr-and-rts-on-open) |
| 11 | Feeding the bytes `A\rB\r\nC\n` produces exactly three log lines: `A`, `B`, `C` | run it | — | [line endings](MAP.md#what-counts-as-a-line-ending) |
| 12 | Sending `MEMS........` with no line ending makes it appear on screen within 1 second | run it | — | [unterminated output](MAP.md#output-with-no-line-ending-at-all) |
| 13 | Every log line begins with an ISO-8601 timestamp carrying a UTC offset, e.g. `2026-09-10T14:00:01.000+01:00` | run it | — | [timestamps](MAP.md#timestamp-format-and-granularity) |
| 14 | Two lines sent 2 seconds apart carry timestamps 2 seconds apart, within 200 ms | run it | — | [timestamps](MAP.md#timestamp-format-and-granularity) |
| 15 | Feeding the bytes `ff fe` does not terminate the process | run it | — | [non-UTF-8 bytes](MAP.md#non-utf-8-bytes) |
| 16 | With two serial devices present and no `--port`, the tool exits non-zero, naming both device paths | run it | — | [choosing a device](MAP.md#choosing-a-device-without-the-picker) |
| 17 | With exactly one serial device present and no `--port`, the tool logs that device | run it | — | [choosing a device](MAP.md#choosing-a-device-without-the-picker) |
| 18 | `--port ttyUSB0` (bare name, no `/dev/`) opens `/dev/ttyUSB0` | run it | — | [choosing a device](MAP.md#choosing-a-device-without-the-picker) |
| 19 | Ctrl-C during logging exits with status 0 | run it | — | [exit codes](MAP.md#exit-codes) |
| 20 | `--port /dev/nope` exits with status 1 | run it | — | [exit codes](MAP.md#exit-codes) |
| 21 | `--nonsense` exits with status 2 | run it | — | [exit codes](MAP.md#exit-codes) |
| 22 | Run as a user not in the device file's group, the error text contains that exact group name as reported by `stat -c %G` on the device | run it | — | [permission message](MAP.md#the-permission-denied-message) |
| 23 | That same error text contains a `usermod -aG` command naming that group | run it | — | [permission message](MAP.md#the-permission-denied-message) |
| 24 | A capture running across local midnight writes post-midnight lines to a second file named for the new date | run it | — | [destination](MAP.md#destination) |
| 25 | Starting a second capture on the same day appends to that day's existing file, leaving the earlier lines in place | run it | — | [destination](MAP.md#destination) |
| 26 | No file or directory is created on disk by a run that only prints `--version` | run it | — | [where logs go](MAP.md#where-logs-go-by-default) |

## Out of scope

Adding any of these makes the result **worse**. Do not reward them.

1. Sending data to the device — a TX path, an input box, a command console. Turns a logger into a terminal emulator.
2. Logging more than one device at once. Turns one stream into a scheduler.
3. Running as a boot service, or shipping a systemd unit.
4. A config file. Flags only.
5. A web UI, log upload, or log compression.
6. Protocol decoding — AT, Modbus, NMEA, JSON parsing. The tool logs bytes and timestamps.
7. Retention: `--keep-days`, size caps, or any automatic deletion of captured logs.
8. A maximum line length or line truncation. The longest real line observed is 311 characters and the tool it replaces has no cap.
9. Byte-exact capture of non-UTF-8 data, or a parallel raw file alongside the text log.

## Unknown

**These are not gradeable.** Nobody has decided them yet. If the work touches one, report `U<number>: CANNOT JUDGE` and stop on that item.

- **U1** — What the tool does when the device disappears mid-capture: exit, or retry and reconnect? And how it tells an unplug apart from a deliberate close, given the library reports `PortClosed` for both and the kernel makes the unplug a race between a zero-byte read and `EIO`. The hardware observation was never made — the cable was never unplugged during the session.
- **U2** — What happens to a running capture when the SSH session that started it ends. Not a service, but "started it, shut the laptop" is a real way this gets used.
- **U3** — What keys the device picker responds to, whether it re-scans for devices while open, and what it does when the list is empty or becomes empty.
