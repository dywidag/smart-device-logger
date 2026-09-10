package main

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// stampLayout is the per-line timestamp. Milliseconds and the offset are kept
// because two devices logged on one machine are compared by wall clock.
const stampLayout = "2006-01-02T15:04:05.000Z07:00"

// idleFlush is how long an unterminated fragment waits before it is logged
// anyway. The device leaves selftest steps such as MEMS........ hanging
// until the result arrives; 200 ms matches the web logger's idle flush.
const idleFlush = 200 * time.Millisecond

// maxLine caps the pending buffer. A device that talks without ever sending
// a terminator — the wrong baud rate, wedged firmware, noise on the lead —
// would otherwise be held in memory in full: the pending slice doubles as it
// grows, so 256 MiB of terminator-free traffic costs about 770 MiB of heap
// and kills the process on a Pi long before it fills the disk. The idle
// flush is no defence, because it only fires on a read that returns nothing.
//
// 64 KiB is 200 times the longest line the device is known to send (a
// 311-character +COPS answer), so reaching it means something is wrong
// rather than verbose.
const maxLine = 64 << 10

// cutMark is appended to a line the cap ended, so the record explains its
// own shape instead of looking like a device that emits 64 KiB lines.
var cutMark = []byte(fmt.Sprintf(" --- cut at %d bytes, line continues ---", maxLine))

// lineSplitter cuts a byte stream into lines on \r\n, lone \n and lone \r,
// dropping the terminator. Bytes after the last terminator stay pending
// until the next chunk or an idle flush, up to maxLine.
//
// A \r ends the line the moment it arrives, and a \n straight after it is
// swallowed so CRLF still counts once. That gives the same lines as holding
// the \r back until the next read, without delaying a lone-CR line by a
// whole read timeout.
type lineSplitter struct {
	pending   []byte
	skipLF    bool // the last byte seen was \r, so a following \n is the rest of CRLF
	overflown bool // the cap just ended a line, so the next terminator is not a blank one
}

// push feeds chunk in, calling emit once per completed line with the line's
// bytes. The slice passed to emit is only valid until emit returns.
func (s *lineSplitter) push(chunk []byte, emit func([]byte) error) error {
	for _, b := range chunk {
		switch b {
		case '\n':
			if s.skipLF {
				s.skipLF = false
				continue
			}
			if err := s.cut(emit); err != nil {
				return err
			}
		case '\r':
			if err := s.cut(emit); err != nil {
				return err
			}
			s.skipLF = true
		default:
			s.skipLF, s.overflown = false, false
			s.pending = append(s.pending, b)
			if len(s.pending) >= maxLine {
				if err := s.overflow(emit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// flush emits whatever is pending as a line of its own, or nothing if the
// stream is at a line boundary.
func (s *lineSplitter) flush(emit func([]byte) error) error {
	if len(s.pending) == 0 {
		return nil
	}
	return s.cut(emit)
}

func (s *lineSplitter) cut(emit func([]byte) error) error {
	if len(s.pending) == 0 && s.overflown {
		// The cap has already emitted these bytes; the terminator that
		// finally arrives ends nothing and is not a blank line.
		s.overflown = false
		return nil
	}
	err := emit(s.pending)
	s.pending = s.pending[:0]
	return err
}

// overflow emits a line the cap ended, marked, and keeps the splitter at a
// line boundary so the bytes that follow start a fresh line.
func (s *lineSplitter) overflow(emit func([]byte) error) error {
	s.pending = append(s.pending, cutMark...)
	err := emit(s.pending)
	s.pending = s.pending[:0]
	s.overflown = true
	return err
}

// record renders one log line: the stamp, a space, the line decoded as
// lossy UTF-8, and a newline. Each invalid byte becomes U+FFFD so the file
// stays valid UTF-8 whatever the wire carried; byte-exact capture is not a
// goal.
func record(at time.Time, line []byte) []byte {
	out := make([]byte, 0, len(stampLayout)+len(line)+2)
	out = at.AppendFormat(out, stampLayout)
	out = append(out, ' ')
	out = appendValidUTF8(out, line)
	return append(out, '\n')
}

func appendValidUTF8(out, line []byte) []byte {
	if utf8.Valid(line) {
		return append(out, line...)
	}
	for len(line) > 0 {
		r, size := utf8.DecodeRune(line)
		if r == utf8.RuneError && size == 1 {
			out = utf8.AppendRune(out, utf8.RuneError)
		} else {
			out = append(out, line[:size]...)
		}
		line = line[size:]
	}
	return out
}
