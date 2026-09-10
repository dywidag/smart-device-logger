package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clock returns a now function whose value the test can move.
func clock(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

func mustWrite(t *testing.T, w *DailyWriter, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("Write(%q): %v", s, err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestDailyWriter(t *testing.T) {
	t.Run("names the file after the day and creates the directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "not", "created", "yet")
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)

		w := NewDailyWriter(dir, "session")
		w.now = clock(&at)
		defer w.Close()

		mustWrite(t, w, "hello\n")

		want := filepath.Join(dir, "session-2026-03-04.log")
		if got := w.Path(); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
		if got := readFile(t, want); got != "hello\n" {
			t.Errorf("file = %q, want %q", got, "hello\n")
		}
	})

	t.Run("creates nothing until the first write", func(t *testing.T) {
		dir := t.TempDir()
		w := NewDailyWriter(dir, "session")
		defer w.Close()

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("directory has %d entries, want 0", len(entries))
		}
		if got := w.Path(); got != "" {
			t.Errorf("Path() = %q, want empty", got)
		}
	})

	t.Run("rolls over to a new file on a new day", func(t *testing.T) {
		dir := t.TempDir()
		at := time.Date(2026, 3, 4, 23, 59, 0, 0, time.Local)

		w := NewDailyWriter(dir, "session")
		w.now = clock(&at)
		defer w.Close()

		mustWrite(t, w, "before\n")
		at = at.Add(2 * time.Minute) // past midnight
		mustWrite(t, w, "after\n")

		if got := readFile(t, filepath.Join(dir, "session-2026-03-04.log")); got != "before\n" {
			t.Errorf("first day = %q, want %q", got, "before\n")
		}
		if got := readFile(t, filepath.Join(dir, "session-2026-03-05.log")); got != "after\n" {
			t.Errorf("second day = %q, want %q", got, "after\n")
		}
	})

	t.Run("appends to an existing file for the same day", func(t *testing.T) {
		dir := t.TempDir()
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
		name := filepath.Join(dir, "session-2026-03-04.log")

		first := NewDailyWriter(dir, "session")
		first.now = clock(&at)
		mustWrite(t, first, "run one\n")
		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		second := NewDailyWriter(dir, "session")
		second.now = clock(&at)
		defer second.Close()
		mustWrite(t, second, "run two\n")

		want := "run one\nrun two\n"
		if got := readFile(t, name); got != want {
			t.Errorf("file = %q, want %q", got, want)
		}
	})

	t.Run("close is safe before a write and when repeated", func(t *testing.T) {
		w := NewDailyWriter(t.TempDir(), "session")

		if err := w.Close(); err != nil {
			t.Errorf("Close before write: %v", err)
		}
		mustWrite(t, w, "x\n")
		if err := w.Close(); err != nil {
			t.Errorf("first Close: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})
}

func TestDefaultLogDir(t *testing.T) {
	t.Run("uses XDG_STATE_HOME when set", func(t *testing.T) {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)

		w := NewDailyWriter("", "session")
		if got, want := w.Dir(), filepath.Join(state, "smart-device-logger"); got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.local/state and ignores a relative XDG_STATE_HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "relative/state")

		w := NewDailyWriter("", "session")
		want := filepath.Join(home, ".local", "state", "smart-device-logger")
		if got := w.Dir(); got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})

	t.Run("creates nothing under HOME until the first write, then lands there from any cwd", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "")
		t.Chdir(t.TempDir())
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)

		w := NewDailyWriter("", "session")
		w.now = clock(&at)
		defer w.Close()

		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Fatalf("HOME has %d entries before the first write, want 0", len(entries))
		}
		mustWrite(t, w, "line\n")

		want := filepath.Join(home, ".local", "state", "smart-device-logger", "session-2026-03-04.log")
		if got := readFile(t, want); got != "line\n" {
			t.Errorf("file = %q, want %q", got, "line\n")
		}
	})

	t.Run("reports a missing HOME on the first write, not at construction", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_STATE_HOME", "")

		w := NewDailyWriter("", "session")
		if _, err := w.Write([]byte("x\n")); err == nil {
			t.Fatal("Write with no HOME succeeded, want an error")
		}
		if got := w.Path(); got != "" {
			t.Errorf("Path() = %q, want empty", got)
		}
	})
}
