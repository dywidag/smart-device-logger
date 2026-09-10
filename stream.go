package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// stampLayout is the per-line timestamp. Milliseconds and the offset are kept
// because two devices logged on one machine are compared by wall clock.
const stampLayout = "2006-01-02T15:04:05.000Z07:00"

// stream copies src to dst one line at a time, stamping each line with the
// time it was read. It returns nil at end of input, and ctx.Err() if the
// context is cancelled between lines.
//
// A bufio.Reader is used rather than a bufio.Scanner because a device that
// emits a long burst with no newline must not be a hard error: ReadString
// returns what it has at EOF, and the buffer grows instead of failing at
// Scanner's 64 KiB line limit.
func stream(ctx context.Context, dst io.Writer, src io.Reader, now func() time.Time) error {
	reader := bufio.NewReader(src)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, readErr := reader.ReadString('\n')
		if line != "" {
			if _, err := dst.Write(record(now(), line)); err != nil {
				return fmt.Errorf("write log line: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read device: %w", readErr)
		}
	}
}

// record renders one log line. The device's own line ending is dropped so a
// CRLF device and an LF device produce identical files.
func record(at time.Time, line string) []byte {
	line = string(bytes.TrimRight([]byte(line), "\r\n"))

	out := make([]byte, 0, len(stampLayout)+len(line)+2)
	out = at.AppendFormat(out, stampLayout)
	out = append(out, ' ')
	out = append(out, line...)
	return append(out, '\n')
}
