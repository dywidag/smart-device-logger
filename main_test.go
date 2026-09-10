package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	t.Run("writes the same stamped lines to the screen and the daily file", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer

		args := []string{"--log-dir", dir, "--log-prefix", "device"}
		if err := run(context.Background(), args, strings.NewReader("ping\npong\n"), &stdout, &stderr); err != nil {
			t.Fatalf("run: %v", err)
		}

		screen := stdout.String()
		if lines := strings.Count(screen, "\n"); lines != 2 {
			t.Errorf("screen has %d lines, want 2: %q", lines, screen)
		}
		if !strings.Contains(screen, " ping\n") || !strings.Contains(screen, " pong\n") {
			t.Errorf("screen = %q, want both stamped lines", screen)
		}

		name := filepath.Join(dir, "device-"+time.Now().Format(dayLayout)+".log")
		if got := readFile(t, name); got != screen {
			t.Errorf("file = %q, want the screen output %q", got, screen)
		}
	})

	t.Run("prints the version and logs nothing", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer

		args := []string{"--version", "--log-dir", dir}
		if err := run(context.Background(), args, strings.NewReader("ignored\n"), &stdout, &stderr); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := strings.TrimSpace(stdout.String()); got != version {
			t.Errorf("stdout = %q, want %q", got, version)
		}
		if entries, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(entries) != 0 {
			t.Errorf("wrote %v, want no log files", entries)
		}
	})

	t.Run("rejects an unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		err := run(context.Background(), []string{"--nope"}, strings.NewReader(""), &stdout, &stderr)

		if err == nil {
			t.Fatal("run succeeded, want an error")
		}
	})

	t.Run("treats Ctrl-C as a clean stop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var stdout, stderr bytes.Buffer
		args := []string{"--log-dir", t.TempDir()}

		if err := run(ctx, args, strings.NewReader("ping\n"), &stdout, &stderr); err != nil {
			t.Errorf("run: %v, want nil", err)
		}
	})

	t.Run("returns on Ctrl-C while the device is silent", func(t *testing.T) {
		// An io.Pipe with nothing written to it is a device that has gone
		// quiet: the read blocks indefinitely.
		silent, writer := io.Pipe()
		defer writer.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var stdout, stderr bytes.Buffer
		args := []string{"--log-dir", t.TempDir()}

		returned := make(chan error, 1)
		go func() { returned <- run(ctx, args, silent, &stdout, &stderr) }()

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
}
