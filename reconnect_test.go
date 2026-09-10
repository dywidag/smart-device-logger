package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePort hands over one chunk and then fails the way a pulled USB lead
// does: go.bug.st/serial reports a hangup as a closed port, not as EOF.
type fakePort struct {
	data []byte
	err  error

	mu     sync.Mutex
	sent   bool
	closed bool
}

func (p *fakePort) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errors.New("port has been closed")
	}
	if !p.sent && len(p.data) > 0 {
		p.sent = true
		return copy(b, p.data), nil
	}
	if p.err != nil {
		return 0, p.err
	}
	return 0, io.EOF
}

func (p *fakePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakePort) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// blockingPort never answers and never ends, like a real port with no
// timeout set: only a Close gets a reader out of it.
type blockingPort struct {
	release chan struct{}
	once    sync.Once
}

func newBlockingPort() *blockingPort {
	return &blockingPort{release: make(chan struct{})}
}

func (p *blockingPort) Read([]byte) (int, error) {
	<-p.release
	return 0, errors.New("port has been closed")
}

func (p *blockingPort) Close() error {
	p.once.Do(func() { close(p.release) })
	return nil
}

// quick makes the backoff invisible to a test without changing its shape.
func quick(open opener, onOpen func(string), report func(string)) *reconnect {
	return &reconnect{
		open:   open,
		min:    time.Millisecond,
		max:    2 * time.Millisecond,
		onOpen: onOpen,
		report: report,
	}
}

func TestCapture(t *testing.T) {
	t.Run("reopens after a disconnect, marks the log and follows the new path", func(t *testing.T) {
		first := &fakePort{data: []byte("before\n")}
		second := &fakePort{data: []byte("after\n")}
		attempts := 0
		var opened []string

		open := func() (io.ReadCloser, string, error) {
			attempts++
			if attempts < 3 {
				return nil, "", errors.New("no such device")
			}
			return second, "/dev/ttyUSB1", nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		out := &syncBuffer{}
		done := make(chan error, 1)
		go func() {
			done <- capture(ctx, out, fixedClock(), first, "/dev/ttyUSB0",
				quick(open, func(p string) { opened = append(opened, p) }, nil))
		}()

		// The second port ends too, so wait for its line and then stop.
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(out.String(), " after\n") {
			if time.Now().After(deadline) {
				t.Fatalf("never logged the reconnected device: %q", out.String())
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("capture: %v", err)
		}

		log := out.String()
		for _, want := range []string{
			stamp + " before\n",
			"--- device disconnected: /dev/ttyUSB0 (",
			stamp + " --- reconnected to /dev/ttyUSB1 ---\n",
			stamp + " after\n",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("log does not contain %q:\n%s", want, log)
			}
		}
		if attempts < 3 {
			t.Errorf("gave up after %d attempts", attempts)
		}
		if len(opened) == 0 || opened[0] != "/dev/ttyUSB1" {
			t.Errorf("onOpen got %v, want the new path first", opened)
		}
		if !first.isClosed() {
			t.Error("the lost port was not closed; a reconnect loop would leak one per unplug")
		}
	})

	t.Run("an end of input is a disconnect, not a clean stop", func(t *testing.T) {
		gone := &fakePort{} // no data, immediate EOF
		reopened := make(chan struct{})
		open := func() (io.ReadCloser, string, error) {
			select {
			case <-reopened:
			default:
				close(reopened)
			}
			return nil, "", errors.New("still gone")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		out := &syncBuffer{}
		done := make(chan error, 1)
		go func() { done <- capture(ctx, out, fixedClock(), gone, "/dev/ttyUSB0", quick(open, nil, nil)) }()

		select {
		case <-reopened:
		case <-time.After(5 * time.Second):
			t.Fatal("EOF did not trigger a reopen")
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("capture: %v", err)
		}
		if !strings.Contains(out.String(), "device disconnected") {
			t.Errorf("log has no disconnect marker: %q", out.String())
		}
	})

	t.Run("retrying forever reports itself once, not once per attempt", func(t *testing.T) {
		var reports []string
		var mu sync.Mutex
		open := func() (io.ReadCloser, string, error) { return nil, "", errors.New("no such device") }
		report := func(text string) {
			mu.Lock()
			defer mu.Unlock()
			reports = append(reports, text)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var out strings.Builder
		if err := capture(ctx, &out, fixedClock(), &fakePort{}, "/dev/ttyUSB0", quick(open, nil, report)); err != nil {
			t.Fatalf("capture: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		// One "reconnecting" for the disconnect, one "cannot reopen" for
		// the first failed attempt, and nothing for the dozens after it.
		if len(reports) != 2 {
			t.Errorf("reported %d times over 100ms of retries: %v", len(reports), reports)
		}
		if markers := strings.Count(out.String(), "device disconnected"); markers != 1 {
			t.Errorf("wrote %d disconnect markers, want 1", markers)
		}
	})

	t.Run("a write failure is fatal and no reopen is attempted", func(t *testing.T) {
		boom := errors.New("no space left on device")
		attempts := 0
		open := func() (io.ReadCloser, string, error) {
			attempts++
			return &fakePort{}, "/dev/ttyUSB1", nil
		}

		port := &fakePort{data: []byte("line\n")}
		err := capture(context.Background(), failingWriter{boom}, fixedClock(), port, "/dev/ttyUSB0", quick(open, nil, nil))

		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
		if attempts != 0 {
			t.Errorf("reopened %d times after a failed write, want 0", attempts)
		}
		if !port.isClosed() {
			t.Error("the port was left open")
		}
	})

	t.Run("without reconnect a disconnect is returned", func(t *testing.T) {
		port := &fakePort{data: []byte("line\n"), err: errors.New("port has been closed")}
		var out strings.Builder

		err := capture(context.Background(), &out, fixedClock(), port, "/dev/ttyUSB0", nil)

		if err == nil || !strings.Contains(err.Error(), "port has been closed") {
			t.Errorf("err = %v, want the read failure", err)
		}
		if got := out.String(); got != stamp+" line\n" {
			t.Errorf("log = %q, want the line logged before the failure", got)
		}
	})

	t.Run("without reconnect an end of input is still an error", func(t *testing.T) {
		err := capture(context.Background(), io.Discard, fixedClock(), &fakePort{}, "/dev/ttyUSB0", nil)

		if err == nil || !strings.Contains(err.Error(), "stopped sending") {
			t.Errorf("err = %v, want a device-gone error", err)
		}
	})

	t.Run("a cancelled context closes a blocked read and returns nil", func(t *testing.T) {
		port := newBlockingPort()
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			done <- capture(ctx, io.Discard, fixedClock(), port, "/dev/ttyUSB0",
				quick(func() (io.ReadCloser, string, error) { return newBlockingPort(), "/dev/ttyUSB0", nil }, nil, nil))
		}()

		time.Sleep(10 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("capture: %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("capture did not return; the read was never woken")
		}
	})
}

func TestReopenBackoffIsCapped(t *testing.T) {
	waits := 0
	open := func() (io.ReadCloser, string, error) {
		waits++
		if waits < 8 {
			return nil, "", errors.New("no such device")
		}
		return &fakePort{}, "/dev/ttyUSB0", nil
	}
	r := &reconnect{open: open, min: time.Millisecond, max: 4 * time.Millisecond}

	start := time.Now()
	if _, _, err := r.reopen(context.Background(), "/dev/ttyUSB0"); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// 1+2+4 then 4 each: doubling without the ceiling would be
	// 1+2+4+8+16+32+64+128 = 255ms.
	if took := time.Since(start); took > 150*time.Millisecond {
		t.Errorf("eight attempts took %s, want the %s ceiling to hold", took, 4*time.Millisecond)
	}
	if waits != 8 {
		t.Errorf("opened %d times, want 8", waits)
	}
}
