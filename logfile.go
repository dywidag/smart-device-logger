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

// DailyWriter appends to <dir>/<prefix>-YYYY-MM-DD.log and switches to a new
// file the first time it is written to on a new local day. It never rotates on
// a timer, so a logger that sits idle over midnight leaves no empty file.
//
// It is safe for concurrent use: the serial reader and the UI can share one.
type DailyWriter struct {
	dir    string
	prefix string
	now    func() time.Time // swapped in tests to drive the rollover

	mu   sync.Mutex
	file *os.File
	day  string
}

// NewDailyWriter returns a writer for dir. Neither the directory nor the first
// file is created until the first Write, so constructing one has no side
// effects on disk.
func NewDailyWriter(dir, prefix string) *DailyWriter {
	return &DailyWriter{dir: dir, prefix: prefix, now: time.Now}
}

// Write appends p to today's file, opening or rolling it over as needed.
func (w *DailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

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
