// Package fakedev is a serial device that does not exist.
//
// It allocates a Linux pseudo-terminal pair and hands back the slave path —
// a real character device that opens, configures and reads exactly like
// /dev/ttyUSB0. Write to the Device and the bytes arrive at whatever opened
// that path. Close it and the reader sees the same hangup a physical unplug
// produces.
//
// This exists so the whole tool can be verified without the FTDI lead
// plugged in: line endings, unterminated fragments, long silences and
// disconnects are all reproducible, and reproducible at will rather than
// once every time somebody walks past the desk.
package fakedev

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Device is one end of a pseudo-terminal pair. The program under test opens
// Path; the test writes to the Device.
type Device struct {
	// Path is the slave device, e.g. /dev/pts/7. Pass it as --port.
	Path string

	master *os.File
	slave  *os.File
}

// Open allocates a pty pair and puts the slave in raw mode.
//
// Raw mode is not cosmetic. A default line discipline translates CR to NL on
// input, which would silently turn the device's lone-CR lines into LF and
// make a line-ending test pass for the wrong reason.
func Open() (*Device, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	dev := &Device{master: master}

	var unlock int32 // 0 = unlocked
	if err := ioctl(master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		master.Close()
		return nil, fmt.Errorf("unlock pty: %w", err)
	}

	var index uint32
	if err := ioctl(master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&index))); err != nil {
		master.Close()
		return nil, fmt.Errorf("read pty number: %w", err)
	}
	dev.Path = fmt.Sprintf("/dev/pts/%d", index)

	// Hold the slave open so the pair survives the program under test
	// closing and reopening it — a reconnect test needs the path to still
	// be there on the second attempt.
	slave, err := os.OpenFile(dev.Path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open %s: %w", dev.Path, err)
	}
	dev.slave = slave

	if err := makeRaw(slave.Fd()); err != nil {
		dev.Close()
		return nil, fmt.Errorf("raw mode: %w", err)
	}
	return dev, nil
}

// Write sends bytes to whatever has the slave open. Send terminators
// explicitly — nothing is appended, because which terminator arrives is
// exactly what several tests are about.
func (d *Device) Write(p []byte) (int, error) { return d.master.Write(p) }

// Send is Write for a string, for readable tests.
func (d *Device) Send(s string) error {
	_, err := d.Write([]byte(s))
	return err
}

// Unplug closes the master, which hangs up the slave the way pulling a USB
// lead does. The reader gets a hangup rather than an endless quiet.
func (d *Device) Unplug() error {
	if d.master == nil {
		return nil
	}
	err := d.master.Close()
	d.master = nil
	return err
}

// Close releases both ends.
func (d *Device) Close() error {
	err := d.Unplug()
	if d.slave != nil {
		if slaveErr := d.slave.Close(); err == nil {
			err = slaveErr
		}
		d.slave = nil
	}
	return err
}

func ioctl(fd, request, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, arg); errno != 0 {
		return errno
	}
	return nil
}

// makeRaw clears the input, output and line processing that would otherwise
// rewrite the bytes in transit.
func makeRaw(fd uintptr) error {
	var t syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t))); err != nil {
		return err
	}

	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0

	return ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(&t)))
}
