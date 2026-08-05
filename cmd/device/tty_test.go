//go:build linux

package device

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"abc":       `'abc'`,
		"a b":       `'a b'`,
		"":          `''`,
		"a'b":       `'a'\''b'`,       // single quote must break out and re-enter
		"'; reboot": `''\''; reboot'`, // injection attempt stays one literal arg
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// openPTY returns the slave path of a fresh pty pair plus its master fd, so a
// test can stand in for a serial port without hardware.
func openPTY(t *testing.T) (master int, slavePath string) {
	t.Helper()
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(m, unix.TIOCSPTLCK, 0); err != nil {
		unix.Close(m)
		t.Skipf("TIOCSPTLCK failed: %v", err)
	}
	n, err := unix.IoctlGetInt(m, unix.TIOCGPTN)
	if err != nil {
		unix.Close(m)
		t.Skipf("TIOCGPTN failed: %v", err)
	}
	t.Cleanup(func() { unix.Close(m) })
	return m, fmt.Sprintf("/dev/pts/%d", n)
}

// TestSetPortRawKeepsPortPollable guards the Ctrl+A Ctrl+X exit path: the
// interactive session ends by closing the port to unblock the goroutine reading
// from it. That only works while the port is registered with the runtime
// poller, so configuring termios must not reach for os.File.Fd(), which drops
// it. When it regressed, the session hung on exit instead of returning.
func TestSetPortRawKeepsPortPollable(t *testing.T) {
	_, slave := openPTY(t)

	f, err := os.OpenFile(slave, os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", slave, err)
	}

	if prev := setPortRaw(f); prev == nil {
		t.Fatal("setPortRaw returned no previous termios")
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 64)
		for {
			if _, err := f.Read(buf); err != nil {
				return
			}
		}
	}()

	// Let the goroutine block in Read before pulling the port out from under it.
	time.Sleep(100 * time.Millisecond)
	f.Close()

	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not interrupt the pending Read: port is no longer pollable, interactive() would hang on exit")
	}
}
