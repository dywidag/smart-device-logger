package main

import (
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

// lineSplitter cuts a byte stream into lines on \r\n, lone \n and lone \r,
// dropping the terminator. Bytes after the last terminator stay pending
// until the next chunk or an idle flush. There is no line length cap: the
// pending buffer grows to whatever the device sends.
//
// A \r ends the line the moment it arrives, and a \n straight after it is
// swallowed so CRLF still counts once. That gives the same lines as holding
// the \r back until the next read, without delaying a lone-CR line by a
// whole read timeout.
type lineSplitter struct {
	pending []byte
	skipLF  bool // the last byte seen was \r, so a following \n is the rest of CRLF
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
			s.skipLF = false
			s.pending = append(s.pending, b)
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
	err := emit(s.pending)
	s.pending = s.pending[:0]
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
