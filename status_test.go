package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonnyasmith/smart-device-logger/internal/fakedev"
)

// fakeClock is a clock the test moves by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestStatus(term *bytes.Buffer, width int) (*status, *fakeClock) {
	clock := &fakeClock{time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)}
	var w io.Writer
	if term != nil {
		w = term
	}
	st := newStatus(w, func() int { return width }, clock.now,
		"/dev/ttyUSB0", "FTDI TTL232R", "115200 8N1",
		func() string { return "/home/pi/.local/state/smart-device-logger/session-2026-09-10.log" })
	return st, clock
}

func TestStatusText(t *testing.T) {
	st, clock := newTestStatus(nil, 0)

	// Before any line, the wait counts from the start of the capture.
	clock.t = clock.t.Add(14 * time.Minute)
	want := "┌ /dev/ttyUSB0  FTDI TTL232R  115200 8N1\n" +
		"└ waiting 14m · 0 lines · 0 B · /home/pi/.local/state/smart-device-logger/session-2026-09-10.log\n"
	if got := st.text(); got != want {
		t.Errorf("text() =\n%s\nwant\n%s", got, want)
	}

	// One Write is one line; bytes come from the reader, not the record.
	src := st.count(io.NopCloser(strings.NewReader("BOOT OK\r\nTEMP=21.4 HUM=48\r\n")))
	if _, err := io.ReadAll(src); err != nil {
		t.Fatal(err)
	}
	st.Write(record(clock.t, []byte("BOOT OK")))
	clock.t = clock.t.Add(time.Second)
	st.Write(record(clock.t, []byte("TEMP=21.4 HUM=48")))
	clock.t = clock.t.Add(2*time.Hour + 5*time.Minute)

	want = "┌ /dev/ttyUSB0  FTDI TTL232R  115200 8N1\n" +
		"└ waiting 2h05m · 2 lines · 27 B · /home/pi/.local/state/smart-device-logger/session-2026-09-10.log\n"
	if got := st.text(); got != want {
		t.Errorf("text() =\n%s\nwant\n%s", got, want)
	}
}

func TestStatusDraw(t *testing.T) {
	var term bytes.Buffer
	st, _ := newTestStatus(&term, 0)

	st.redraw()
	first := term.String()
	if strings.Contains(first, "\x1b[1A") {
		t.Errorf("first draw moved the cursor up before anything was on screen: %q", first)
	}
	if !strings.Contains(first, "┌ /dev/ttyUSB0  FTDI TTL232R  115200 8N1\n└ waiting 0s") {
		t.Errorf("first draw = %q, want the two-line block", first)
	}

	term.Reset()
	st.redraw()
	if second := term.String(); !strings.HasPrefix(second, "\r\x1b[1A\x1b[J\x1b[2m┌") {
		t.Errorf("redraw = %q, want the cursor moved up over the old block", second)
	}

	// A data line goes through the screen writer: the block is erased,
	// the line written to stdout untouched, and the block drawn again.
	var stdout bytes.Buffer
	term.Reset()
	if _, err := st.screen(&stdout).Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "line\n" {
		t.Errorf("stdout = %q, want the bare line", stdout.String())
	}
	if out := term.String(); !strings.HasPrefix(out, "\r\x1b[1A\x1b[J") || !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("terminal = %q, want erase then redraw", out)
	}

	for _, forbidden := range []string{"\x1b[?1049h", "\x1b[2J", "\x1b[3J"} {
		if strings.Contains(term.String(), forbidden) {
			t.Errorf("terminal output contains %q, which destroys scrollback", forbidden)
		}
	}
}

func TestStatusRunEndsOnItsOwnLine(t *testing.T) {
	var term bytes.Buffer
	st, _ := newTestStatus(&term, 0)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() { st.run(done); close(stopped) }()
	time.Sleep(20 * time.Millisecond)
	close(done)
	<-stopped
	if out := term.String(); !strings.HasSuffix(out, "\x1b[0m\n") {
		t.Errorf("run left the terminal at %q, want the block followed by a newline", out)
	}
}

func TestStatusWithoutTerminalDrawsNothing(t *testing.T) {
	st, _ := newTestStatus(nil, 0)
	var stdout bytes.Buffer
	if w := st.screen(&stdout); w != io.Writer(&stdout) {
		t.Error("screen() wrapped stdout although there is no terminal to draw on")
	}
	done := make(chan struct{})
	close(done)
	st.run(done) // must return at once rather than tick
	st.redraw()  // and never touch a nil terminal
}

func TestStatusClipsToTerminalWidth(t *testing.T) {
	var term bytes.Buffer
	st, _ := newTestStatus(&term, 20)
	st.redraw()
	for _, line := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(term.String(), "\x1b[2m"), "\x1b[0m"), "\n") {
		if n := len([]rune(line)); n >= 20 {
			t.Errorf("line %q is %d cells wide, must be under 20 so it cannot wrap", line, n)
		}
	}
}

func TestWaiting(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                     "0s",
		59*time.Second + 900*time.Millisecond: "59s",
		time.Minute:                           "1m",
		29*time.Minute + 59*time.Second:       "29m",
		time.Hour:                             "1h00m",
		25*time.Hour + 7*time.Minute:          "25h07m",
	}
	for d, want := range cases {
		if got := waiting(d); got != want {
			t.Errorf("waiting(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestByteCount(t *testing.T) {
	cases := map[int64]string{0: "0 B", 23: "23 B", 999: "999 B", 1000: "1.0 kB", 1350: "1.4 kB", 2_500_000: "2.5 MB"}
	for n, want := range cases {
		if got := byteCount(n); got != want {
			t.Errorf("byteCount(%d) = %q, want %q", n, got, want)
		}
	}
	if got := plural(1, "line"); got != "1 line" {
		t.Errorf("plural(1) = %q", got)
	}
}

func TestTildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	inside := filepath.Join(home, ".local", "state", "x.log")
	if got := tildePath(inside); got != "~/.local/state/x.log" {
		t.Errorf("tildePath(%q) = %q", inside, got)
	}
	if got := tildePath("/tmp/x.log"); got != "/tmp/x.log" {
		t.Errorf("tildePath(/tmp/x.log) = %q", got)
	}
	if got := tildePath(home + "2/x.log"); got != home+"2/x.log" {
		t.Errorf("tildePath(%q) = %q, a sibling of home is not under it", home+"2/x.log", got)
	}
}

func TestAdapterName(t *testing.T) {
	dev := openFake(t)
	if got := adapterName(dev.Path); got != "" {
		t.Errorf("adapterName(pty) = %q, want none", got)
	}
	if got := adapterName("/dev/nope"); got != "" {
		t.Errorf("adapterName(missing) = %q, want none", got)
	}
	if _, err := os.Stat("/dev/ttyUSB0"); err == nil {
		if got := adapterName("/dev/ttyUSB0"); !strings.Contains(got, " ") {
			t.Errorf("adapterName(/dev/ttyUSB0) = %q, want manufacturer and product", got)
		}
	}
}

func TestTerminal(t *testing.T) {
	if terminal(&bytes.Buffer{}) != nil {
		t.Error("a buffer was taken for a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if terminal(f) != nil {
		t.Error("a regular file was taken for a terminal")
	}
	if terminalWidth(f.Fd()) != 0 {
		t.Error("a regular file reported a width")
	}

	// The fake device's slave is a real tty, so it is what the block draws on.
	dev, err := fakedev.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	tty, err := os.OpenFile(dev.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	if terminal(tty) == nil {
		t.Error("a pty slave was not taken for a terminal")
	}
}
