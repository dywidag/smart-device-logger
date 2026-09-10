// Command smart-device-logger streams data from a USB serial device, shows it
// on screen, and appends it to a log file per day.
//
//	smart-device-logger --port /dev/ttyUSB0
//
// With one device attached, --port can be left out. Data lines go to stdout
// and the day's file; everything else goes to stderr, so stdout can be piped.
// Exit status is 0 after Ctrl-C, 1 on any error and 2 for bad usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.bug.st/serial"
)

// version lives in version.go, where the build stamping rule is.

func main() {
	// NotifyContext turns the first Ctrl-C into a cancelled context, and
	// leaves the second one to the default handler so a wedged read can
	// still be killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(exitStatus(err, os.Stderr))
}

// usageError marks a bad command line, which exits 2 rather than 1 so a
// script can tell a typo from a device that failed.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// exitStatus maps run's result to the process exit code, printing the
// error where one is due. The flag package has already printed usage
// errors and help, so neither is repeated.
func exitStatus(err error, stderr io.Writer) int {
	var usage usageError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &usage):
		return 2
	}
	fmt.Fprintf(stderr, "smart-device-logger: %v\n", err)
	return 1
}

// run holds everything main does apart from the exit code, so the tests can
// drive the real entry point with their own arguments and streams.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("smart-device-logger", flag.ContinueOnError)
	flags.SetOutput(stderr)

	portName := flags.String("port", "", "serial device: a path, a bare name such as ttyUSB0, or a /dev/serial/by-id entry (default: the only device present)")
	logDir := flags.String("log-dir", "", "directory to write daily log files into (default ~/.local/state/smart-device-logger)")
	prefix := flags.String("log-prefix", "session", "leading part of each log file name")
	keepDays := flags.Int("keep-days", 0, "delete daily files older than this many days at each rollover (0 = keep everything)")
	readTimeout := flags.Duration("read-timeout", 200*time.Millisecond, "how long a read waits for data before checking for Ctrl-C")
	reconnecting := flags.Bool("reconnect", true, "reopen the device after an unplug instead of exiting")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}
	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q", flags.Arg(0))
		fmt.Fprintln(stderr, err)
		flags.Usage()
		return usageError{err}
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildVersion())
		return nil
	}

	// resolve names the device to open, now and after every unplug: a
	// given --port as it stands, and otherwise whatever single device is
	// present, because a replugged adapter comes back as a different
	// /dev/ttyUSBn while its by-id link stays put.
	resolve := func() (device, error) {
		if *portName != "" {
			path := devicePath(*portName)
			return device{path: path, target: path}, nil
		}
		devices, err := discover(byIDDir, serial.GetPortsList)
		if err != nil {
			return device{}, err
		}
		return pick(devices)
	}

	chosen, err := resolve()
	if err != nil {
		return err
	}
	if *portName == "" {
		fmt.Fprintf(stderr, "using the only serial device present: %s\n", chosen)
	}
	path := chosen.path

	port, err := openPort(path, *readTimeout)
	if err != nil {
		return err
	}

	daily := NewDailyWriter(*logDir, *prefix, *keepDays)
	defer daily.Close()

	fmt.Fprintf(stderr, "%s open at 115200 8N1 (Ctrl-C to stop)\n", path)

	// The status block draws on stderr only when it is a terminal, so a
	// pipe on stdout sees nothing but data lines and a redirected stderr
	// sees no escape sequences at all.
	term := terminal(stderr)
	width := func() int { return 0 }
	if f, ok := term.(*os.File); ok {
		width = func() int { return terminalWidth(f.Fd()) }
	}
	logPath := func() string {
		if p := daily.Path(); p != "" {
			return p
		}
		return filepath.Join(daily.Dir(), fmt.Sprintf("%s-%s.log", *prefix, time.Now().Format(dayLayout)))
	}
	st := newStatus(term, width, time.Now, path, adapterName(path), "115200 8N1", logPath)
	statusDone := make(chan struct{})
	statusStopped := make(chan struct{})
	go func() { st.run(statusDone); close(statusStopped) }()
	defer func() { close(statusDone); <-statusStopped }()

	// Notes go through the same writer as data lines so they land above
	// the status block rather than through the middle of it.
	notes := st.screen(stderr)

	var reopen *reconnect
	if *reconnecting {
		reopen = &reconnect{
			open: func() (io.ReadCloser, string, error) {
				next, err := resolve()
				if err != nil {
					return nil, "", err
				}
				port, err := openPort(next.path, *readTimeout)
				if err != nil {
					return nil, "", err
				}
				return st.count(port), next.path, nil
			},
			onOpen: func(path string) { st.setDevice(path, adapterName(path)) },
			report: func(text string) { fmt.Fprintf(notes, "smart-device-logger: %s\n", text) },
		}
	}

	// capture owns the port from here, including closing it: closing is
	// how a read blocked in the driver is woken on Ctrl-C, and how a lost
	// device is let go of before the next open. The bounded wait keeps the
	// exit prompt even if a read is wedged. Nothing already written is
	// lost: DailyWriter has no buffer of its own.
	done := make(chan error, 1)
	go func() {
		done <- capture(ctx, io.MultiWriter(st.screen(stdout), daily, st), time.Now, st.count(port), path, reopen)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		case <-time.After(time.Second):
		}
		return nil
	}
}
