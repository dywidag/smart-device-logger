package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jonnyasmith/smart-device-logger/internal/fakedev"
)

// syncBuffer is a bytes.Buffer the test can read while run is writing it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor polls until want appears in buf, or fails the test.
func waitFor(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("output never contained %q: %q", want, buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func openFake(t *testing.T) *fakedev.Device {
	t.Helper()
	dev, err := fakedev.Open()
	if err != nil {
		t.Fatalf("fakedev: %v", err)
	}
	t.Cleanup(func() { dev.Close() })
	return dev
}

func TestRun(t *testing.T) {
	t.Run("logs the device to the screen and the daily file", func(t *testing.T) {
		dev := openFake(t)
		dir := t.TempDir()
		var stdout, stderr syncBuffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		args := []string{"--port", dev.Path, "--log-dir", dir, "--log-prefix", "device"}
		go func() { returned <- run(ctx, args, &stdout, &stderr) }()

		waitFor(t, &stderr, dev.Path+" open at 115200 8N1")
		if err := dev.Send("ping\npong\n"); err != nil {
			t.Fatalf("send: %v", err)
		}
		waitFor(t, &stdout, " pong\n")
		cancel()
		if err := <-returned; err != nil {
			t.Fatalf("run: %v", err)
		}

		screen := stdout.String()
		if lines := strings.Count(screen, "\n"); lines != 2 {
			t.Errorf("screen has %d lines, want 2: %q", lines, screen)
		}
		if !strings.Contains(screen, " ping\n") {
			t.Errorf("screen = %q, want both stamped lines", screen)
		}
		name := filepath.Join(dir, "device-"+time.Now().Format(dayLayout)+".log")
		if got := readFile(t, name); got != screen {
			t.Errorf("file = %q, want the screen output %q", got, screen)
		}
	})

	t.Run("opens a bare name under /dev", func(t *testing.T) {
		dev := openFake(t)
		var stdout, stderr syncBuffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		args := []string{"--port", strings.TrimPrefix(dev.Path, "/dev/"), "--log-dir", t.TempDir()}
		go func() { returned <- run(ctx, args, &stdout, &stderr) }()

		waitFor(t, &stderr, dev.Path+" open")
		cancel()
		if err := <-returned; err != nil {
			t.Errorf("run: %v", err)
		}
	})

	t.Run("prints the version and logs nothing", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer

		args := []string{"--version", "--log-dir", dir}
		if err := run(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := strings.TrimSpace(stdout.String()); got != buildVersion() {
			t.Errorf("stdout = %q, want %q", got, buildVersion())
		}
		if entries, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(entries) != 0 {
			t.Errorf("wrote %v, want no log files", entries)
		}
	})

	t.Run("bad usage is a usage error", func(t *testing.T) {
		for _, args := range [][]string{{"--nope"}, {"stray"}} {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), args, &stdout, &stderr)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Errorf("run(%v) = %v, want a usageError", args, err)
			}
		}
	})

	t.Run("a missing device is an error, not bad usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), []string{"--port", "/dev/nope"}, &stdout, &stderr)
		var usage usageError
		if err == nil || errors.As(err, &usage) {
			t.Errorf("run = %v, want a plain error", err)
		}
	})

	t.Run("returns on Ctrl-C while the device is silent", func(t *testing.T) {
		dev := openFake(t)
		var stdout, stderr syncBuffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		args := []string{"--port", dev.Path, "--log-dir", t.TempDir()}
		go func() { returned <- run(ctx, args, &stdout, &stderr) }()

		waitFor(t, &stderr, "open at")
		cancel()
		select {
		case err := <-returned:
			if err != nil {
				t.Errorf("run: %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("run did not return after the context was cancelled")
		}
	})

	t.Run("an unplug does not end the capture", func(t *testing.T) {
		dev := openFake(t)
		dir := t.TempDir()
		var stdout, stderr syncBuffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		args := []string{"--port", dev.Path, "--log-dir", dir, "--log-prefix", "device"}
		go func() { returned <- run(ctx, args, &stdout, &stderr) }()

		waitFor(t, &stderr, dev.Path+" open at")
		if err := dev.Send("before\n"); err != nil {
			t.Fatalf("send: %v", err)
		}
		waitFor(t, &stdout, " before\n")

		// Pull the lead. The path itself goes with it, so every reopen
		// fails: what is being tested is that the tool keeps trying
		// instead of exiting, and says so in the log.
		if err := dev.Unplug(); err != nil {
			t.Fatalf("unplug: %v", err)
		}
		waitFor(t, &stdout, "--- device disconnected: "+dev.Path)
		waitFor(t, &stderr, "reconnecting")

		select {
		case err := <-returned:
			t.Fatalf("run returned %v after an unplug, want it still capturing", err)
		case <-time.After(750 * time.Millisecond):
		}

		cancel()
		if err := <-returned; err != nil {
			t.Errorf("run: %v", err)
		}
		// The marker is in the day's file too, so the record explains its
		// own gap without the terminal being watched.
		name := filepath.Join(dir, "device-"+time.Now().Format(dayLayout)+".log")
		if got := readFile(t, name); !strings.Contains(got, "--- device disconnected: ") {
			t.Errorf("file has no disconnect marker:\n%s", got)
		}
	})

	t.Run("--reconnect=false makes an unplug a failure", func(t *testing.T) {
		dev := openFake(t)
		var stdout, stderr syncBuffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		args := []string{"--port", dev.Path, "--log-dir", t.TempDir(), "--reconnect=false"}
		go func() { returned <- run(ctx, args, &stdout, &stderr) }()

		waitFor(t, &stderr, dev.Path+" open at")
		if err := dev.Unplug(); err != nil {
			t.Fatalf("unplug: %v", err)
		}

		select {
		case err := <-returned:
			if err == nil {
				t.Error("run returned nil after an unplug, want an error and exit 1")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run did not return after the device went away")
		}
	})
}

func TestExitStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{flag.ErrHelp, 0},
		{usageError{errors.New("bad flag")}, 2},
		{errors.New("open failed"), 1},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		if got := exitStatus(c.err, &stderr); got != c.want {
			t.Errorf("exitStatus(%v) = %d, want %d", c.err, got, c.want)
		}
		if c.want == 1 && !strings.Contains(stderr.String(), c.err.Error()) {
			t.Errorf("stderr = %q, want the error", stderr.String())
		}
	}
}

func TestDevicePath(t *testing.T) {
	cases := map[string]string{
		"ttyUSB0":             "/dev/ttyUSB0",
		"pts/6":               "/dev/pts/6",
		"/dev/ttyUSB0":        "/dev/ttyUSB0",
		"/dev/serial/by-id/x": "/dev/serial/by-id/x",
	}
	for in, want := range cases {
		if got := devicePath(in); got != want {
			t.Errorf("devicePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscover(t *testing.T) {
	a, b := openFake(t), openFake(t)
	byID := t.TempDir()
	link := filepath.Join(byID, "usb-Fake_A-if00-port0")
	if err := os.Symlink(a.Path, link); err != nil {
		t.Fatal(err)
	}
	list := func() ([]string, error) { return []string{a.Path, b.Path}, nil }

	devices, err := discover(byID, list)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	want := []device{{link, a.Path}, {b.Path, b.Path}}
	if len(devices) != 2 || devices[0] != want[0] || devices[1] != want[1] {
		t.Fatalf("discover = %v, want %v", devices, want)
	}

	t.Run("a missing by-id directory is not an error", func(t *testing.T) {
		devices, err := discover(filepath.Join(byID, "absent"), list)
		if err != nil || len(devices) != 2 {
			t.Errorf("discover = %v, %v; want the two listed ports", devices, err)
		}
	})

	t.Run("pick refuses unless exactly one", func(t *testing.T) {
		if _, err := pick(nil); err == nil {
			t.Error("pick(none) succeeded")
		}
		if got, err := pick(devices[:1]); err != nil || got != want[0] {
			t.Errorf("pick(one) = %v, %v", got, err)
		}
		_, err := pick(devices)
		if err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), b.Path) {
			t.Errorf("pick(two) = %v, want an error naming both", err)
		}
	})
}

func TestOpenPort(t *testing.T) {
	t.Run("permission denied names the file's group and the fix", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root is never denied")
		}
		path := filepath.Join(t.TempDir(), "locked")
		if err := os.WriteFile(path, nil, 0); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		gid := info.Sys().(*syscall.Stat_t).Gid
		group := strconv.Itoa(int(gid))
		if g, err := user.LookupGroupId(group); err == nil {
			group = g.Name
		}

		_, err := openPort(path, time.Second)
		if err == nil {
			t.Fatal("openPort succeeded on an unreadable file")
		}
		for _, want := range []string{path, "permission denied", "group " + group, "usermod -aG " + group + " "} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	})

	t.Run("deasserts DTR and RTS and tolerates a device without them", func(t *testing.T) {
		dev := openFake(t)
		port, err := openPort(dev.Path, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("openPort: %v", err)
		}
		defer port.Close()

		// A pseudo-terminal answers ENOTTY to modem-line ioctls; opening it
		// must still succeed and the timeout must be in force.
		start := time.Now()
		n, err := port.Read(make([]byte, 16))
		if n != 0 || err != nil {
			t.Errorf("silent read = %d, %v; want a 0, nil timeout", n, err)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("silent read took %s, want the 50ms timeout", took)
		}
	})

	t.Run("a missing file is reported plainly", func(t *testing.T) {
		_, err := openPort("/dev/nope", time.Second)
		if err == nil || !strings.Contains(err.Error(), "open /dev/nope") || strings.Contains(err.Error(), "usermod") {
			t.Errorf("error = %v", err)
		}
	})
}
