// Command fakedev is a serial device for testing, with no hardware.
//
// It prints the path of a real character device and then behaves like a
// VoidCellular board on the other end of it:
//
//	go run ./cmd/fakedev                    # replay the boot transcript, then idle
//	go run ./cmd/fakedev -script silence    # open and say nothing, ever
//	go run ./cmd/fakedev -script endings    # A\rB\r\nC\n — one line per terminator
//	go run ./cmd/fakedev -script fragment   # MEMS........ with no terminator
//	go run ./cmd/fakedev -script garbage    # invalid UTF-8 bytes
//	go run ./cmd/fakedev -script boot -unplug 10s
//
// Point the logger at the printed path:
//
//	smart-device-logger --port /dev/pts/7
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jonnyasmith/smart-device-logger/internal/fakedev"
)

func main() {
	script := flag.String("script", "boot", "boot | silence | endings | fragment | garbage")
	unplug := flag.Duration("unplug", 0, "disconnect this long after starting (0 = never)")
	repeat := flag.Duration("repeat", 0, "replay the script on this interval (0 = once)")
	flag.Parse()

	dev, err := fakedev.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakedev:", err)
		os.Exit(1)
	}
	defer dev.Close()

	fmt.Println(dev.Path)
	fmt.Fprintf(os.Stderr, "fakedev: %s ready, script=%s. Ctrl-C to stop.\n", dev.Path, *script)

	if *unplug > 0 {
		go func() {
			time.Sleep(*unplug)
			fmt.Fprintf(os.Stderr, "fakedev: unplugging after %s\n", *unplug)
			dev.Unplug()
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			if err := play(dev, *script); err != nil {
				fmt.Fprintln(os.Stderr, "fakedev:", err)
				return
			}
			if *repeat == 0 {
				return
			}
			time.Sleep(*repeat)
		}
	}()

	<-stop
	fmt.Fprintln(os.Stderr, "fakedev: closing")
}

func play(dev *fakedev.Device, script string) error {
	switch script {
	case "boot":
		return fakedev.Replay(dev, fakedev.Boot)
	case "silence":
		return nil
	case "endings":
		// One line per terminator: CR, CRLF, LF.
		return dev.Send("A\rB\r\nC\n")
	case "fragment":
		// Unterminated, as the selftest does. Only an idle flush shows it.
		return dev.Send("MEMS........")
	case "garbage":
		_, err := dev.Write([]byte{0xff, 0xfe, '\r', '\n'})
		return err
	default:
		return fmt.Errorf("unknown script %q", script)
	}
}
