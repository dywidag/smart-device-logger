// Command smart-device-logger streams data from a serial device, shows it on
// screen, and appends it to a log file per day.
//
// This is the skeleton. Today it logs whatever arrives on standard input, so
// the whole path — read, stamp, screen, daily file, clean shutdown — is real
// and testable:
//
//	cat /dev/ttyUSB0 | smart-device-logger --log-dir ~/logs
//
// Device discovery, the picker UI and opening the port are the next pieces.
// See README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags)" .
var version = "dev"

func main() {
	// NotifyContext turns the first Ctrl-C into a cancelled context, and
	// leaves the second one to the default handler so a wedged read can
	// still be killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "smart-device-logger: %v\n", err)
		os.Exit(1)
	}
}

// run holds everything main does apart from the exit code, so the tests can
// drive the real entry point with their own arguments and streams.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("smart-device-logger", flag.ContinueOnError)
	flags.SetOutput(stderr)

	logDir := flags.String("log-dir", "logs", "directory to write daily log files into")
	prefix := flags.String("log-prefix", "session", "leading part of each log file name")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}

	daily := NewDailyWriter(*logDir, *prefix)
	defer daily.Close()

	fmt.Fprintf(stderr, "logging to %s (Ctrl-C to stop)\n", *logDir)

	// stream only notices cancellation between lines, and a quiet device can
	// leave it blocked in a read for as long as it likes. Racing it against
	// the context is what makes Ctrl-C answer at once. Abandoning the reader
	// is safe: DailyWriter writes straight to the file with no buffer of its
	// own, so nothing already read is lost when the process exits. Once a
	// real port is opened, give it a read deadline so the read itself wakes
	// up and this becomes belt and braces.
	done := make(chan error, 1)
	go func() { done <- stream(ctx, io.MultiWriter(stdout, daily), stdin, time.Now) }()

	select {
	case err := <-done:
		if ctx.Err() != nil {
			// A cancelled context is how the tool is meant to stop,
			// not a failure.
			return nil
		}
		return err
	case <-ctx.Done():
		return nil
	}
}
