package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dayLayout names a log file after the local calendar day it was opened on.
const dayLayout = "2006-01-02"

// stateSubdir is this tool's directory under the XDG state home.
const stateSubdir = "smart-device-logger"

// DailyWriter appends to <dir>/<prefix>-YYYY-MM-DD.log and switches to a new
// file the first time it is written to on a new local day. It never rotates on
// a timer, so a logger that sits idle over midnight leaves no empty file.
//
// It is safe for concurrent use: the serial reader and the UI can share one.
type DailyWriter struct {
	dir    string
	prefix string
	now    func() time.Time // swapped in tests to drive the rollover
	dirErr error            // why dir could not be resolved; reported by Write

	mu   sync.Mutex
	file *os.File
	day  string
}

// NewDailyWriter returns a writer for dir. An empty dir means the XDG state
// directory for this tool, so where a capture lands never depends on the
// shell's working directory. Neither the directory nor the first file is
// created until the first Write, so constructing one has no side effects on
// disk.
func NewDailyWriter(dir, prefix string) *DailyWriter {
	w := &DailyWriter{dir: dir, prefix: prefix, now: time.Now}
	if dir == "" {
		w.dir, w.dirErr = defaultLogDir()
	}
	return w
}

// defaultLogDir is $XDG_STATE_HOME/smart-device-logger, falling back to
// ~/.local/state/smart-device-logger as the XDG base directory spec says. A
// relative XDG_STATE_HOME is ignored, also per the spec: it would put the
// file back at the mercy of the working directory.
func defaultLogDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, stateSubdir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve default log directory: %w (pass --log-dir)", err)
	}
	return filepath.Join(home, ".local", "state", stateSubdir), nil
}

// Dir reports the directory daily files are written into, after any default
// has been resolved. It is known before the first Write, so the status block
// can name where the capture will land.
func (w *DailyWriter) Dir() string { return w.dir }

// Write appends p to today's file, opening or rolling it over as needed.
func (w *DailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.dirErr != nil {
		return 0, w.dirErr
	}
	day := w.now().Format(dayLayout)
	if w.file == nil || day != w.day {
		if err := w.open(day); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

// Close closes the current file. It is safe to call on a writer that never
// wrote anything, and safe to call twice.
func (w *DailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file, w.day = nil, ""
	return err
}

// Path reports the file currently open, or "" before the first Write.
func (w *DailyWriter) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return ""
	}
	return w.file.Name()
}

// open switches to the file for day. The old file is closed first, so a
// rollover cannot leak a descriptor. Opening is append-only, so restarting on
// the same day continues the day's file rather than truncating it.
func (w *DailyWriter) open(day string) error {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	name := filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.prefix, day))
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	if w.file != nil {
		_ = w.file.Close()
	}
	w.file, w.day = file, day
	return nil
}
