package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dayLayout names a log file after the local calendar day it was opened on.
const dayLayout = "2006-01-02"

// stateSubdir is this tool's directory under the XDG state home.
const stateSubdir = "smart-device-logger"

// syncEvery bounds how much of the record a power cut can take. Lines reach
// the kernel immediately but sit in the page cache until the filesystem gets
// round to them, so a Pi that loses power mid-capture loses the tail of the
// day's file. A sync every 5 s bounds that without syncing per line: at this
// device's rate that is a few flushes a minute, which an SD card does not
// notice, and it is driven by writes rather than a timer so an idle logger
// stays asleep.
const syncEvery = 5 * time.Second

// DailyWriter appends to <dir>/<prefix>-YYYY-MM-DD.log and switches to a new
// file the first time it is written to on a new local day. It never rotates on
// a timer, so a logger that sits idle over midnight leaves no empty file.
//
// It is safe for concurrent use: the serial reader and the UI can share one.
type DailyWriter struct {
	dir      string
	prefix   string
	keepDays int              // delete files older than this many days; 0 keeps every file
	now      func() time.Time // swapped in tests to drive the rollover
	sync     func(*os.File) error
	dirErr   error // why dir could not be resolved; reported by Write

	mu       sync.Mutex
	file     *os.File
	day      string
	lastSync time.Time
}

// NewDailyWriter returns a writer for dir. An empty dir means the XDG state
// directory for this tool, so where a capture lands never depends on the
// shell's working directory. keepDays deletes daily files older than that
// many days at each rollover; 0 keeps every file, because deleting captured
// data has to be asked for. Neither the directory nor the first file is
// created until the first Write, so constructing one has no side effects on
// disk.
func NewDailyWriter(dir, prefix string, keepDays int) *DailyWriter {
	w := &DailyWriter{
		dir:      dir,
		prefix:   prefix,
		keepDays: keepDays,
		now:      time.Now,
		sync:     (*os.File).Sync,
	}
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

// Write appends p to today's file, opening or rolling it over as needed, and
// flushes to disk at most every syncEvery.
func (w *DailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.dirErr != nil {
		return 0, w.dirErr
	}
	at := w.now()
	day := at.Format(dayLayout)
	if w.file == nil || day != w.day {
		if err := w.open(day); err != nil {
			return 0, err
		}
		w.lastSync = at
	}
	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	if at.Sub(w.lastSync) >= syncEvery {
		w.lastSync = at
		// A failed sync is the disk telling us the record is not safe,
		// which is a capture-stopping error like a failed write.
		if err := w.sync(w.file); err != nil {
			return n, fmt.Errorf("flush log file: %w", err)
		}
	}
	return n, nil
}

// Close flushes and closes the current file. It is safe to call on a writer
// that never wrote anything, and safe to call twice.
func (w *DailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.sync(w.file)
	if closeErr := w.file.Close(); err == nil {
		err = closeErr
	}
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

// open switches to the file for day. The old file is flushed and closed
// first, so a rollover cannot leak a descriptor or leave the finished day
// unsafe on disk. Opening is append-only, so restarting on the same day
// continues the day's file rather than truncating it.
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
		_ = w.sync(w.file)
		_ = w.file.Close()
	}
	w.file, w.day = file, day
	w.prune(day)
	return nil
}

// prune deletes this prefix's daily files from before the keepDays window
// ending on day. Only names this writer could have written are considered,
// so nothing else in the directory is at risk, and a file that will not
// delete is left alone: retention is housekeeping, and housekeeping must
// never be the reason a capture stops.
func (w *DailyWriter) prune(day string) {
	if w.keepDays <= 0 {
		return
	}
	today, err := time.ParseInLocation(dayLayout, day, time.Local)
	if err != nil {
		return
	}
	oldest := today.AddDate(0, 0, -(w.keepDays - 1))

	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stamp, ok := w.dayOf(e.Name())
		if !ok || !stamp.Before(oldest) {
			continue
		}
		_ = os.Remove(filepath.Join(w.dir, e.Name()))
	}
}

// dayOf reads the date out of one of this writer's file names, and reports
// false for anything else in the directory.
func (w *DailyWriter) dayOf(name string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, w.prefix+"-")
	if !ok {
		return time.Time{}, false
	}
	rest, ok = strings.CutSuffix(rest, ".log")
	if !ok {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(dayLayout, rest, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}
