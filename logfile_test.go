package main

import (
	"errors"
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

		w := NewDailyWriter(dir, "session", 0)
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
		w := NewDailyWriter(dir, "session", 0)
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

		w := NewDailyWriter(dir, "session", 0)
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

		first := NewDailyWriter(dir, "session", 0)
		first.now = clock(&at)
		mustWrite(t, first, "run one\n")
		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		second := NewDailyWriter(dir, "session", 0)
		second.now = clock(&at)
		defer second.Close()
		mustWrite(t, second, "run two\n")

		want := "run one\nrun two\n"
		if got := readFile(t, name); got != want {
			t.Errorf("file = %q, want %q", got, want)
		}
	})

	t.Run("close is safe before a write and when repeated", func(t *testing.T) {
		w := NewDailyWriter(t.TempDir(), "session", 0)

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

	t.Run("flushes to disk on a throttle, at each rollover and at close", func(t *testing.T) {
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
		w := NewDailyWriter(t.TempDir(), "session", 0)
		w.now = clock(&at)
		syncs := 0
		w.sync = func(f *os.File) error {
			syncs++
			return f.Sync()
		}

		mustWrite(t, w, "first\n") // opens the file; the clock starts here
		mustWrite(t, w, "second\n")
		if syncs != 0 {
			t.Errorf("synced %d times in the first instant, want 0: a sync per line wears an SD card out", syncs)
		}

		at = at.Add(syncEvery)
		mustWrite(t, w, "third\n")
		if syncs != 1 {
			t.Errorf("synced %d times after %s, want 1", syncs, syncEvery)
		}

		at = at.Add(24 * time.Hour) // rollover flushes the finished day
		mustWrite(t, w, "next day\n")
		if syncs != 2 {
			t.Errorf("synced %d times across the rollover, want 2", syncs)
		}

		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if syncs != 3 {
			t.Errorf("synced %d times including the close, want 3", syncs)
		}
	})

	t.Run("a failed flush is reported, not swallowed", func(t *testing.T) {
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
		w := NewDailyWriter(t.TempDir(), "session", 0)
		w.now = clock(&at)
		boom := errors.New("input/output error")
		w.sync = func(*os.File) error { return boom }
		defer w.Close()

		mustWrite(t, w, "first\n")
		at = at.Add(syncEvery)
		if _, err := w.Write([]byte("second\n")); !errors.Is(err, boom) {
			t.Errorf("Write = %v, want %v", err, boom)
		}
	})

	t.Run("keep-days deletes only this prefix's older files, at rollover", func(t *testing.T) {
		dir := t.TempDir()
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)

		// Two of this prefix's days, one older, and three files that are
		// not ours: another prefix, another shape, and a directory.
		keep := filepath.Join(dir, "session-2026-03-02.log")
		old := filepath.Join(dir, "session-2026-03-01.log")
		other := filepath.Join(dir, "device-2026-01-01.log")
		odd := filepath.Join(dir, "session-notes.log")
		for _, name := range []string{keep, old, other, odd} {
			if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		sub := filepath.Join(dir, "session-2026-02-02.log.d")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}

		w := NewDailyWriter(dir, "session", 3) // today and the two days before it
		w.now = clock(&at)
		defer w.Close()
		mustWrite(t, w, "today\n")

		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("%s survived, want it deleted", filepath.Base(old))
		}
		for _, name := range []string{keep, other, odd, sub} {
			if _, err := os.Stat(name); err != nil {
				t.Errorf("%s was deleted: %v", filepath.Base(name), err)
			}
		}
	})

	t.Run("keep-days off deletes nothing", func(t *testing.T) {
		dir := t.TempDir()
		at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
		ancient := filepath.Join(dir, "session-2020-01-01.log")
		if err := os.WriteFile(ancient, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		w := NewDailyWriter(dir, "session", 0)
		w.now = clock(&at)
		defer w.Close()
		mustWrite(t, w, "today\n")

		if _, err := os.Stat(ancient); err != nil {
			t.Errorf("a file from 2020 was deleted with retention off: %v", err)
		}
	})
}

func TestDefaultLogDir(t *testing.T) {
	t.Run("uses XDG_STATE_HOME when set", func(t *testing.T) {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)

		w := NewDailyWriter("", "session", 0)
		if got, want := w.Dir(), filepath.Join(state, "smart-device-logger"); got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.local/state and ignores a relative XDG_STATE_HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "relative/state")

		w := NewDailyWriter("", "session", 0)
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

		w := NewDailyWriter("", "session", 0)
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

		w := NewDailyWriter("", "session", 0)
		if _, err := w.Write([]byte("x\n")); err == nil {
			t.Fatal("Write with no HOME succeeded, want an error")
		}
		if got := w.Path(); got != "" {
			t.Errorf("Path() = %q, want empty", got)
		}
	})
}
