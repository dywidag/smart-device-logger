package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// writeError marks a failure to write a record — a full disk, a read-only
// mount, a closed pipe. It is told apart from a read failure because the two
// deserve opposite answers: a device that stops talking is reconnected to,
// while a log that cannot be written must stop the capture loudly rather
// than spin reopening a device whose data has nowhere to go.
type writeError struct{ err error }

func (e writeError) Error() string { return e.err.Error() }
func (e writeError) Unwrap() error { return e.err }

// stream copies src to dst one line at a time, stamping each line with the
// time it was read. It returns nil at end of input, and ctx.Err() if the
// context is cancelled between reads. Whatever is pending when it returns
// is logged first, so a fragment is not lost to Ctrl-C or an unplug.
//
// src is read straight into a byte slice, never through bufio: a serial
// port with a read timeout answers (0, nil) when the device is quiet, and
// bufio.Reader gives up after 100 of those. Here an empty read is simply
// the idle tick that decides whether a pending fragment has waited long
// enough to be shown; there is no count of them and no limit.
func stream(ctx context.Context, dst io.Writer, src io.Reader, now func() time.Time) error {
	var (
		lines    lineSplitter
		buf      = make([]byte, 4096)
		lastData time.Time
	)
	emit := func(line []byte) error {
		if _, err := dst.Write(record(now(), line)); err != nil {
			return writeError{fmt.Errorf("write log line: %w", err)}
		}
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			if flushErr := lines.flush(emit); flushErr != nil {
				return flushErr
			}
			return err
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			lastData = now()
			if err := lines.push(buf[:n], emit); err != nil {
				return err
			}
		} else if len(lines.pending) > 0 && now().Sub(lastData) >= idleFlush {
			if err := lines.flush(emit); err != nil {
				return err
			}
		}

		if readErr != nil {
			if err := lines.flush(emit); err != nil {
				return err
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read device: %w", readErr)
		}
	}
}
