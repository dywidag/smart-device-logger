package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Backoff between reopen attempts. 250 ms answers the common case — a USB
// re-enumeration, an autosuspend blip — almost immediately, and the 5 s
// ceiling stops a device that is genuinely gone from spinning the CPU or the
// log for the days it might be unplugged.
const (
	reconnectMin = 250 * time.Millisecond
	reconnectMax = 5 * time.Second
)

// opener reopens the device, returning what to read and the path it opened.
// The path is asked for rather than assumed because /dev/ttyUSB0 can come
// back as /dev/ttyUSB1 after a replug.
type opener func() (io.ReadCloser, string, error)

// reconnect is how a capture survives losing its device. A nil *reconnect
// means fail fast: the read error is returned and the process exits, which
// is what a script driving one capture wants.
type reconnect struct {
	open   opener
	min    time.Duration     // first backoff; zero means reconnectMin
	max    time.Duration     // backoff ceiling; zero means reconnectMax
	onOpen func(path string) // told the path of each reopen, so the status block follows it
	report func(string)      // progress for stderr; nil to say nothing
}

// capture streams port to dst until the context is cancelled, reopening the
// device after a read failure when r says to.
//
// It owns port from here on, including closing it. Three outcomes are kept
// apart on purpose:
//
//   - a cancelled context is a clean stop, and returns nil;
//   - a write failure — full disk, read-only mount — is fatal, because
//     reopening a device whose data has nowhere to go just loses it faster;
//   - a read failure is the device going away, which is what r is for.
//
// Each disconnect and reconnect is written into the log as a stamped
// marker, so the record explains its own gap rather than looking like a
// device that went quiet.
func capture(ctx context.Context, dst io.Writer, now func() time.Time, port io.ReadCloser, path string, r *reconnect) error {
	// A read that is already blocked in the driver does not notice a
	// cancelled context; closing the port underneath it does. Without
	// this, Ctrl-C waits out the read timeout, which --read-timeout can
	// make arbitrarily long.
	var (
		mu     sync.Mutex
		active = port
	)
	closeActive := func() {
		mu.Lock()
		defer mu.Unlock()
		if active != nil {
			active.Close()
			active = nil
		}
	}
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			closeActive()
		case <-watchDone:
		}
	}()

	for {
		err := stream(ctx, dst, port, now)
		closeActive()

		switch {
		case ctx.Err() != nil:
			return nil
		case isWriteError(err):
			return err
		case r == nil:
			if err == nil {
				return fmt.Errorf("read device: %s stopped sending (unplugged?)", path)
			}
			return err
		}

		if err := marker(dst, now, disconnected(path, err)); err != nil {
			return err
		}
		if r.report != nil {
			r.report(fmt.Sprintf("%s: %v — reconnecting", path, orGone(err)))
		}

		next, nextPath, err := r.reopen(ctx, path)
		if err != nil {
			// Only a cancelled context ends the retry loop.
			return nil
		}
		port, path = next, nextPath

		mu.Lock()
		active = port
		mu.Unlock()
		if ctx.Err() != nil {
			// Cancelled in the window between the check and the open,
			// so the watcher has already fired and will not fire again.
			closeActive()
			return nil
		}

		if r.onOpen != nil {
			r.onOpen(path)
		}
		if err := marker(dst, now, "reconnected to "+path); err != nil {
			return err
		}
		if r.report != nil {
			r.report("reconnected to " + path)
		}
	}
}

// reopen retries r.open with a capped, doubling backoff until it succeeds or
// the context is cancelled. Only the first failure is reported: a device
// left unplugged overnight must not fill the terminal, and the markers in
// the log already say the capture is down.
func (r *reconnect) reopen(ctx context.Context, path string) (io.ReadCloser, string, error) {
	wait, ceiling := r.min, r.max
	if wait <= 0 {
		wait = reconnectMin
	}
	if ceiling <= 0 {
		ceiling = reconnectMax
	}

	for attempt := 1; ; attempt++ {
		if err := sleep(ctx, wait); err != nil {
			return nil, "", err
		}
		if wait *= 2; wait > ceiling {
			wait = ceiling
		}

		port, opened, err := r.open()
		if err == nil {
			return port, opened, nil
		}
		if attempt == 1 && r.report != nil {
			r.report(fmt.Sprintf("cannot reopen %s: %v — retrying every %s until it comes back", path, err, ceiling))
		}
	}
}

// sleep waits for d, or returns the context's error if it is cancelled first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// marker writes one stamped line of the tool's own into the log, in the same
// shape as a device line so nothing downstream has to treat it specially.
func marker(dst io.Writer, now func() time.Time, text string) error {
	if _, err := dst.Write(record(now(), []byte("--- "+text+" ---"))); err != nil {
		return writeError{fmt.Errorf("write marker: %w", err)}
	}
	return nil
}

// disconnected names the reason in the log marker. The library reports a
// hangup as a closed port rather than EOF, and stream turns a plain end of
// input into nil, so both need saying in words.
func disconnected(path string, err error) string {
	return fmt.Sprintf("device disconnected: %s (%v)", path, orGone(err))
}

func orGone(err error) error {
	if err == nil {
		return errors.New("stopped sending")
	}
	return err
}

func isWriteError(err error) bool {
	var we writeError
	return errors.As(err, &we)
}
