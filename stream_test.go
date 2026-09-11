package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fixedClock stamps every line with the same instant so tests can compare
// whole outputs.
func fixedClock() func() time.Time {
	at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

const stamp = "2026-03-04 09:00:00"

// steppedClock advances by step on every reading, so idle gaps are made by
// the number of reads rather than by sleeping.
func steppedClock(step time.Duration) func() time.Time {
	at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.FixedZone("BST", 3600))
	return func() time.Time {
		at = at.Add(step)
		return at
	}
}

// scriptedReader replays reads one at a time, exactly as a serial port with
// a read timeout would: an empty chunk is a timeout, (0, nil). After the script
// it returns io.EOF.
type scriptedReader struct{ reads [][]byte }

func (r *scriptedReader) Read(p []byte) (int, error) {
	if len(r.reads) == 0 {
		return 0, io.EOF
	}
	chunk := r.reads[0]
	r.reads = r.reads[1:]
	return copy(p, chunk), nil
}

func chunks(s ...string) *scriptedReader {
	r := &scriptedReader{}
	for _, c := range s {
		r.reads = append(r.reads, []byte(c))
	}
	return r
}

func TestStream(t *testing.T) {
	t.Run("stamps every line", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("one\ntwo\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " one\n" + stamp + " two\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("all three terminators end a line and are stripped", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("A\rB\r\nC\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " A\n" + stamp + " B\n" + stamp + " C\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("a CRLF split across reads is one terminator", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, chunks("one\r", "\ntwo\r", "", "", "\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " one\n" + stamp + " two\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("logs a final line with no newline", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("cut off"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " cut off\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("silence of any length is not end of stream", func(t *testing.T) {
		// 10,000 consecutive timeouts is 100 times the bufio.Reader cliff
		// and, at the default 200 ms, over half an hour of quiet.
		r := &scriptedReader{reads: make([][]byte, 10000)}
		r.reads = append(r.reads, []byte("after the gap\n"))
		var out bytes.Buffer

		if err := stream(context.Background(), &out, r, fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " after the gap\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("an unterminated fragment is flushed after an idle gap", func(t *testing.T) {
		// Each clock reading is 150 ms. The second chunk lands at +300;
		// the timeout at +450 has waited 150 ms so holds, the one at
		// +600 has waited 300 ms and flushes, stamping the line at +750.
		var out bytes.Buffer
		r := chunks("MEMS....", "....", "", "", "", "OK\r\n")

		if err := stream(context.Background(), &out, r, steppedClock(150*time.Millisecond)); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := "2026-03-04 09:00:00 MEMS........\n" +
			"2026-03-04 09:00:01 OK\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("a fragment is not flushed before the idle gap", func(t *testing.T) {
		var out bytes.Buffer
		r := chunks("MEMS", "", "....\n")

		if err := stream(context.Background(), &out, r, steppedClock(50*time.Millisecond)); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := "2026-03-04 09:00:00 MEMS....\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("stamps use local date and time without a zone suffix", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("x\n"), steppedClock(time.Second)); err != nil {
			t.Fatalf("stream: %v", err)
		}

		if want := "2026-03-04 09:00:02 x\n"; out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("invalid UTF-8 is replaced, not fatal", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("\xff\xfe\nok \xc3\xa9\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " \uFFFD\uFFFD\n" + stamp + " ok é\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("a long line is passed through whole up to the cap", func(t *testing.T) {
		line := strings.Repeat("x", maxLine-1)
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader(line+"\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		if want := stamp + " " + line + "\n"; out.String() != want {
			t.Errorf("output has %d bytes, want %d", out.Len(), len(want))
		}
	})

	t.Run("a device that never terminates a line is cut at the cap, marked", func(t *testing.T) {
		// 3.5 caps' worth of bytes and no terminator anywhere: the memory
		// this must not hold is what killed a capture on a Pi.
		var out bytes.Buffer
		src := strings.NewReader(strings.Repeat("M", 7*maxLine/2))

		if err := stream(context.Background(), &out, src, fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		records := strings.SplitAfter(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(records) != 4 {
			t.Fatalf("got %d records, want 3 cut lines and the remainder: %d bytes", len(records), out.Len())
		}
		for _, r := range records[:3] {
			if want := len(stamp) + 1 + maxLine + len(cutMark) + 1; len(r) != want {
				t.Errorf("cut record is %d bytes, want %d", len(r), want)
			}
			if !strings.HasSuffix(r, string(cutMark)+"\n") {
				t.Errorf("cut record does not end with the marker: %q", r[len(r)-60:])
			}
		}
		if last := records[3]; len(last) != len(stamp)+1+maxLine/2 {
			t.Errorf("remainder is %d bytes, want the half cap left over", len(last))
		}
	})

	t.Run("the terminator after a cut does not add a blank line", func(t *testing.T) {
		var out bytes.Buffer
		src := strings.NewReader(strings.Repeat("M", maxLine) + "\r\nnext\n")

		if err := stream(context.Background(), &out, src, fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		lines := strings.Count(out.String(), "\n")
		if lines != 2 {
			t.Errorf("got %d records, want the cut line and \"next\": %q", lines, out.String())
		}
		if !strings.HasSuffix(out.String(), stamp+" next\n") {
			t.Errorf("output does not end with the next line: %q", out.String())
		}
	})

	t.Run("a write failure while cutting is reported", func(t *testing.T) {
		boom := errors.New("disk full")

		err := stream(context.Background(), failingWriter{boom}, strings.NewReader(strings.Repeat("M", maxLine+1)), fixedClock())

		if !errors.Is(err, boom) || !isWriteError(err) {
			t.Errorf("err = %v, want a writeError wrapping %v", err, boom)
		}
	})

	t.Run("stops on a cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var out bytes.Buffer
		err := stream(ctx, &out, strings.NewReader("one\n"), fixedClock())

		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		if out.Len() != 0 {
			t.Errorf("output = %q, want nothing", out.String())
		}
	})

	t.Run("reports a read failure after logging what was pending", func(t *testing.T) {
		var out bytes.Buffer
		boom := errors.New("device unplugged")

		err := stream(context.Background(), &out, failingReader{[]byte("half"), boom}, fixedClock())

		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
		if want := stamp + " half\n"; out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("reports a write failure", func(t *testing.T) {
		boom := errors.New("disk full")

		err := stream(context.Background(), failingWriter{boom}, strings.NewReader("one\n"), fixedClock())

		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	})
}

// failingReader hands over its data once and then fails.
type failingReader struct {
	data []byte
	err  error
}

func (r failingReader) Read(p []byte) (int, error) { return copy(p, r.data), r.err }

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
