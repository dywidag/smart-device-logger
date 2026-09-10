package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// status is the two-line block pinned under the stream, redrawn in place:
//
//	┌ /dev/ttyUSB0  FTDI TTL232R  115200 8N1
//	└ waiting 14m · 2 lines · 23 B · ~/.local/state/smart-device-logger/session-2026-09-10.log
//
// It is written to the terminal on stderr and nowhere else, so stdout stays
// a clean stream of data lines whether it is a screen or a pipe. Redrawing
// moves the cursor up over the previous block and overwrites it; nothing
// switches screens or clears the display, so scrollback is untouched.
type status struct {
	term     io.Writer // the terminal, or nil when stderr is not one
	width    func() int
	now      func() time.Time
	port     string
	adapter  string
	settings string
	logPath  func() string
	started  time.Time

	mu    sync.Mutex
	lines int64
	bytes int64
	last  time.Time // when the latest line arrived; zero until the first
	drawn bool      // a block is on screen, so the next draw overwrites it
}

// newStatus describes a capture on port. term is the terminal to draw on,
// or nil to draw nothing. logPath is asked at each redraw, so the block
// follows the day's file across midnight.
func newStatus(term io.Writer, width func() int, now func() time.Time, port, adapter, settings string, logPath func() string) *status {
	return &status{
		term:     term,
		width:    width,
		now:      now,
		port:     port,
		adapter:  adapter,
		settings: settings,
		logPath:  logPath,
		started:  now(),
	}
}

// Write counts one logged line per call. It sits in stream's MultiWriter
// beside stdout and the daily file, where every Write is exactly one record.
func (s *status) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.lines++
	s.last = s.now()
	s.mu.Unlock()
	return len(p), nil
}

// reader counts the bytes the device sends, without touching them.
func (s *status) reader(r io.Reader) io.Reader {
	return countingReader{r, s}
}

type countingReader struct {
	r io.Reader
	s *status
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.s.mu.Lock()
		c.s.bytes += int64(n)
		c.s.mu.Unlock()
	}
	return n, err
}

// screen wraps the stdout writer so a data line lands above the block
// rather than under it: the block is erased, the line written, and the
// block drawn again below. When stdout is a pipe the erase and redraw are
// harmless; the data line never carries any of it.
func (s *status) screen(w io.Writer) io.Writer {
	if s.term == nil {
		return w
	}
	return screenWriter{w, s}
}

type screenWriter struct {
	w io.Writer
	s *status
}

func (sw screenWriter) Write(p []byte) (int, error) {
	sw.s.mu.Lock()
	defer sw.s.mu.Unlock()
	sw.s.erase()
	n, err := sw.w.Write(p)
	sw.s.draw()
	return n, err
}

// run redraws the block every tick until ctx is done, then leaves the last
// block on screen with the cursor on a fresh line for the shell prompt.
func (s *status) run(done <-chan struct{}) {
	if s.term == nil {
		return
	}
	s.redraw()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			s.redraw()
		case <-done:
			s.mu.Lock()
			s.erase()
			s.draw()
			io.WriteString(s.term, "\n")
			s.mu.Unlock()
			return
		}
	}
}

func (s *status) redraw() {
	if s.term == nil {
		return
	}
	s.mu.Lock()
	s.erase()
	s.draw()
	s.mu.Unlock()
}

// erase removes the block from the screen, leaving the cursor where its
// first line began. Caller holds mu.
func (s *status) erase() {
	if !s.drawn {
		return
	}
	// \r to column 0, up one line, clear from there to the end of the
	// screen. Each block line is clipped to the terminal width, so the
	// block is never more than two rows.
	io.WriteString(s.term, "\r\x1b[1A\x1b[J")
	s.drawn = false
}

// draw writes the block, leaving the cursor at the end of its second line
// so the next erase knows where it is. Caller holds mu.
func (s *status) draw() {
	var b bytes.Buffer
	b.WriteString("\x1b[2m")
	b.WriteString(clip(s.line1(), s.width()))
	b.WriteString("\n")
	b.WriteString(clip(s.line2(), s.width()))
	b.WriteString("\x1b[0m")
	s.term.Write(b.Bytes())
	s.drawn = true
}

func (s *status) line1() string {
	if s.adapter == "" {
		return fmt.Sprintf("┌ %s  %s", s.port, s.settings)
	}
	return fmt.Sprintf("┌ %s  %s  %s", s.port, s.adapter, s.settings)
}

func (s *status) line2() string {
	since := s.last
	if since.IsZero() {
		since = s.started
	}
	return fmt.Sprintf("└ waiting %s · %s · %s · %s",
		waiting(s.now().Sub(since)), plural(s.lines, "line"), byteCount(s.bytes), tildePath(s.logPath()))
}

// text is the block as plain text, one line per row, with no escapes.
func (s *status) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.line1() + "\n" + s.line2() + "\n"
}

// waiting renders a gap the way a person reads a clock: seconds until a
// minute, whole minutes until an hour, then hours and minutes.
func waiting(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func byteCount(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1000*1000))
}

// tildePath shortens a path under $HOME to ~/..., as the prototype shows it.
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return path
	}
	if rel, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rel
	}
	return path
}

// clip cuts a line to width cells so it cannot wrap, which would throw the
// cursor arithmetic in erase off by a row. width 0 means unknown: no clip.
func clip(line string, width int) string {
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width-1 {
		return line
	}
	return string(runes[:width-1])
}

// adapterName reads the USB manufacturer and product of the device behind
// path from sysfs, e.g. "FTDI TTL232R". A device that is not USB, or not a
// tty the kernel knows, gets "".
func adapterName(path string) string {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	dev, err := filepath.EvalSymlinks(filepath.Join("/sys/class/tty", filepath.Base(target), "device"))
	if err != nil {
		return ""
	}
	// The tty sits a few levels below the USB device that carries the
	// descriptors: interface, then device, for a plain FTDI lead.
	for dir := dev; dir != "/" && dir != "/sys"; dir = filepath.Dir(dir) {
		product, err := os.ReadFile(filepath.Join(dir, "product"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(product))
		if maker, err := os.ReadFile(filepath.Join(dir, "manufacturer")); err == nil {
			name = strings.TrimSpace(string(maker)) + " " + name
		}
		return name
	}
	return ""
}

// terminal reports w as the terminal to draw on, or nil when it is not one,
// so a redirected stderr gets no escape sequences either.
func terminal(w io.Writer) io.Writer {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f.Fd()) {
		return nil
	}
	return f
}

func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

// terminalWidth reports the column count of the terminal on fd, or 0 when it
// cannot be read. It is asked at every redraw so a resized window is honoured.
func terminalWidth(fd uintptr) int {
	var ws struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}
