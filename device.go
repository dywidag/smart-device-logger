package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.bug.st/serial"
)

// byIDDir is where udev keeps one symlink per USB serial device, named after
// its serial number. /dev/ttyUSBn is renumbered on a replug; these are not.
const byIDDir = "/dev/serial/by-id"

// device is one serial device the tool could open.
type device struct {
	path   string // what to open and show: the by-id link when there is one
	target string // the character device the path resolves to
}

func (d device) String() string {
	if d.path == d.target {
		return d.path
	}
	return d.path + " -> " + d.target
}

// discover lists the serial devices present, by-id entries first, then the
// ports the library enumerates that no by-id entry already covers. Both
// sources are needed: by-id names a device stably, and the enumerator sees
// devices udev has not linked.
func discover(byID string, list func() ([]string, error)) ([]device, error) {
	var found []device
	seen := map[string]bool{}
	add := func(path string) {
		target, err := filepath.EvalSymlinks(path)
		if err != nil || seen[target] {
			return
		}
		seen[target] = true
		found = append(found, device{path: path, target: target})
	}

	entries, err := os.ReadDir(byID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("list %s: %w", byID, err)
	}
	for _, e := range entries {
		add(filepath.Join(byID, e.Name()))
	}

	ports, err := list()
	if err != nil {
		return nil, fmt.Errorf("list serial ports: %w", err)
	}
	for _, p := range ports {
		add(p)
	}
	return found, nil
}

// pick returns the one device to log, or an error naming every candidate.
// With several present, guessing is worse than refusing: logging the wrong
// device looks like a working capture until someone reads the file.
func pick(devices []device) (device, error) {
	switch len(devices) {
	case 0:
		return device{}, errors.New("no serial device found; plug one in or pass --port")
	case 1:
		return devices[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d serial devices present, pass --port to choose one:", len(devices))
	for _, d := range devices {
		b.WriteString("\n  ")
		b.WriteString(d.String())
	}
	return device{}, errors.New(b.String())
}

// devicePath turns what --port was given into something to open: a bare
// name such as ttyUSB0 lives in /dev; an absolute path is used as is.
func devicePath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return "/dev/" + name
}

// openPort opens path at 115200 8N1 with no flow control, deasserts DTR and
// RTS, and sets the read timeout that lets a blocked read notice Ctrl-C.
//
// Opening a tty raises DTR and RTS; on a board that resets on DTR that is a
// reboot, so both are dropped immediately after the open, as the web logger
// does. A pseudo-terminal has no modem lines and answers ENOTTY, which is
// not a failure.
func openPort(path string, readTimeout time.Duration) (serial.Port, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := serial.Open(path, mode)
	if err != nil {
		return nil, openError(path, err)
	}
	for _, set := range []func(bool) error{port.SetDTR, port.SetRTS} {
		if err := set(false); err != nil && !errors.Is(err, syscall.ENOTTY) {
			port.Close()
			return nil, fmt.Errorf("deassert DTR/RTS on %s: %w", path, err)
		}
	}
	if err := port.SetReadTimeout(readTimeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("set read timeout on %s: %w", path, err)
	}
	return port, nil
}

// openError explains a failed open. Permission denied is the likely first-run
// failure on a Pi, and the fix depends on which group owns the device file —
// dialout on Raspberry Pi OS, uucp on Arch — so the group is read from the
// file rather than guessed.
func openError(path string, err error) error {
	var portErr *serial.PortError
	if !errors.As(err, &portErr) || portErr.Code() != serial.PermissionDenied {
		return fmt.Errorf("open %s: %w", path, err)
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		return fmt.Errorf("open %s: permission denied", path)
	}
	group := strconv.FormatUint(uint64(info.Sys().(*syscall.Stat_t).Gid), 10)
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}
	who := "$USER"
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	return fmt.Errorf("open %s: permission denied; it belongs to group %s, so add yourself to that group and log out and back in:\n  sudo usermod -aG %s %s", path, group, group, who)
}
