package fakedev

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// reader opens the fake device the way the tool under test would.
func reader(t *testing.T, dev *Device) *os.File {
	t.Helper()
	f, err := os.OpenFile(dev.Path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", dev.Path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestDevice(t *testing.T) {
	t.Run("hands back a real device file", func(t *testing.T) {
		dev, err := Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer dev.Close()

		info, err := os.Stat(dev.Path)
		if err != nil {
			t.Fatalf("stat %s: %v", dev.Path, err)
		}
		if info.Mode()&os.ModeCharDevice == 0 {
			t.Errorf("%s is not a character device (mode %v)", dev.Path, info.Mode())
		}
	})

	t.Run("delivers bytes verbatim, including lone CR", func(t *testing.T) {
		// The whole point of raw mode: a default line discipline turns CR
		// into NL and a line-ending test would then pass for the wrong
		// reason.
		dev, err := Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer dev.Close()

		port := reader(t, dev)
		if err := dev.Send("A\rB\r\nC\n"); err != nil {
			t.Fatalf("Send: %v", err)
		}

		got := read(t, port, len("A\rB\r\nC\n"))
		if got != "A\rB\r\nC\n" {
			t.Errorf("read %q, want %q", got, "A\rB\r\nC\n")
		}
	})

	t.Run("stays silent until written to", func(t *testing.T) {
		dev, err := Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer dev.Close()

		port := reader(t, dev)
		if err := port.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
			t.Skipf("read deadlines unsupported here: %v", err)
		}

		buf := make([]byte, 64)
		n, err := port.Read(buf)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("read returned (%d, %v), want a deadline timeout", n, err)
		}
	})

	t.Run("unplug hangs the reader up rather than going quiet", func(t *testing.T) {
		dev, err := Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer dev.Close()

		port := reader(t, dev)
		if err := dev.Send("before\r\n"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if got := read(t, port, len("before\r\n")); got != "before\r\n" {
			t.Fatalf("read %q before unplug", got)
		}

		if err := dev.Unplug(); err != nil {
			t.Fatalf("Unplug: %v", err)
		}

		// A hangup surfaces as EOF or EIO depending on the race the kernel
		// runs; both are an ending, and neither is an endless wait.
		if err := port.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Skipf("read deadlines unsupported here: %v", err)
		}
		buf := make([]byte, 64)
		_, err = port.Read(buf)
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, syscall.EIO):
		case errors.Is(err, os.ErrDeadlineExceeded):
			t.Error("read blocked past the unplug; the reader would hang forever")
		default:
			t.Errorf("read error %v, want EOF or EIO", err)
		}
	})
}

func TestBootTranscript(t *testing.T) {
	t.Run("replays every awkward case the real device produces", func(t *testing.T) {
		var b strings.Builder
		for _, c := range Boot {
			b.WriteString(c.Data)
		}
		all := b.String()

		if !strings.Contains(all, "bin,count,min,max,avg\n") ||
			strings.Contains(all, "bin,count,min,max,avg\r\n") {
			t.Error("the mreport CSV header must arrive with a bare LF")
		}
		if !strings.Contains(all, "AT+COPS=?\r+") {
			t.Error("the AT exchange must contain a lone CR")
		}
		if !strings.Contains(all, "MEMS........OK") {
			t.Error("MEMS........ must be unterminated, so an idle flush is needed to show it")
		}
		if longest := longestLine(all); longest != 311 {
			t.Errorf("longest line is %d characters, want the real 311", longest)
		}
	})

	t.Run("arrives through the device in order", func(t *testing.T) {
		dev, err := Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer dev.Close()

		port := reader(t, dev)
		short := []Chunk{{Data: "one\r\n"}, {Gap: 20 * time.Millisecond, Data: "two\n"}}
		go Replay(dev, short)

		if got := read(t, port, len("one\r\ntwo\n")); got != "one\r\ntwo\n" {
			t.Errorf("read %q, want %q", got, "one\r\ntwo\n")
		}
	})
}

// read collects exactly n bytes, or fails.
func read(t *testing.T, f *os.File, n int) string {
	t.Helper()
	if err := f.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Skipf("read deadlines unsupported here: %v", err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return string(buf)
}

func longestLine(s string) int {
	longest := 0
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}
