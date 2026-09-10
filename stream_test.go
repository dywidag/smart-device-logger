package main

import (
	"bytes"
	"context"
	"errors"
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

const stamp = "2026-03-04T09:00:00.000Z"

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

	t.Run("normalises CRLF endings", func(t *testing.T) {
		var out bytes.Buffer

		if err := stream(context.Background(), &out, strings.NewReader("one\r\n"), fixedClock()); err != nil {
			t.Fatalf("stream: %v", err)
		}

		want := stamp + " one\n"
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

	t.Run("reports a read failure", func(t *testing.T) {
		var out bytes.Buffer
		boom := errors.New("device unplugged")

		err := stream(context.Background(), &out, failingReader{boom}, fixedClock())

		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	})
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
