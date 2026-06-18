//go:build linux

//
// Copyright 2017-2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package device

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/urfave/cli"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var promptRe = regexp.MustCompile(`# $`)

const (
	escapeKey = 0x01 // Ctrl+A
	exitKey   = 0x18 // Ctrl+X
)

// serialProbe opens a serial port, configures 115200 8N1, sends Enter,
// and waits for the Pantavisor debug-shell "# " prompt.
// Returns an open fd or -1 on failure.
func serialProbe(port string) int {
	fd, err := unix.Open(port, unix.O_RDWR, 0)
	if err != nil {
		return -1
	}

	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		unix.Close(fd)
		return -1
	}

	// Raw: 115200, 8N1, no flow control, no CR/NL mapping
	termios.Iflag &^= unix.IXON | unix.IXOFF | unix.IXANY | unix.ICRNL
	termios.Oflag &^= unix.OPOST | unix.OCRNL | unix.ONLCR
	termios.Cflag &^= unix.CBAUD
	termios.Cflag |= unix.B115200 | unix.CS8
	termios.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		unix.Close(fd)
		return -1
	}

	// Probe: send Enter, accumulate all data until "# " appears or timeout.
	// Data arrives character-by-character over USB-serial, so we must
	// keep reading until we see the prompt or 2s pass without data.
	for attempt := range 5 {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		unix.Write(fd, []byte("\r\n"))

		var buf [1]byte
		var allData []byte
		deadline := time.Now().Add(2 * time.Second)

		for time.Now().Before(deadline) {
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			np, _ := unix.Poll(fds, 500)
			if np == 0 {
				continue
			}
			nn, _ := unix.Read(fd, buf[:])
			if nn <= 0 {
				continue
			}
			allData = append(allData, buf[:nn]...)

			// Abort on noisy ports (busybox spam, login prompts)
			if len(allData) > 1000 {
				break
			}

			if promptRe.Match(allData) {
				// Drain any remaining buffered data
				for {
					fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
					nn, _ := unix.Poll(fds, 100)
					if nn == 0 {
						break
					}
					unix.Read(fd, buf[:])
				}
				return fd
			}
		}
	}

	unix.Close(fd)
	return -1
}

// runCommands opens the port, runs each cmd, prints output, returns exit code.
func runCommands(port string, cmds []string) int {
	fd := serialProbe(port)
	if fd < 0 {
		fmt.Fprintf(os.Stderr, "pvr device tty: no Pantavisor debug shell on %s\n", port)
		return 2
	}
	defer unix.Close(fd)

	for _, cmd := range cmds {
		if _, err := unix.Write(fd, []byte(cmd+"\r\n")); err != nil {
			fmt.Fprintf(os.Stderr, "pvr device tty: write failed on %s: %v\n", port, err)
			return 2
		}

		// Collect output until prompt (accumulate data, not just last read)
		var buf [1]byte
		var out []byte
		responded := false
		deadline := time.Now().Add(10 * time.Second)

		for time.Now().Before(deadline) {
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			np, _ := unix.Poll(fds, 500)
			if np == 0 {
				continue
			}
			nn, _ := unix.Read(fd, buf[:])
			if nn <= 0 {
				continue
			}
			out = append(out, buf[:nn]...)

			if promptRe.Match(out) {
				responded = true
				break
			}

			if len(out) > 2000 {
				responded = true // got output, just truncated
				break
			}
		}

		// Strip echoed command line and trailing prompt
		text := string(out)
		lines := strings.Split(text, "\n")
		if len(lines) > 0 {
			// Remove the line echoing our command
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasSuffix(trimmed, cmd) || trimmed == cmd {
					lines = append(lines[:i], lines[i+1:]...)
					break
				}
			}
			// Remove trailing prompt
			if len(lines) > 0 {
				lines = lines[:len(lines)-1]
			}
		}
		result := strings.ReplaceAll(strings.Join(lines, "\n"), "\r", "")
		if result != "" {
			fmt.Println(result)
		}

		if !responded {
			fmt.Fprintf(os.Stderr, "pvr device tty: timeout waiting for response to %q on %s\n", cmd, port)
			return 1
		}
	}
	return 0
}

// shQuote single-quotes s for a POSIX shell, escaping embedded single quotes.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// scanPorts finds USB serial ports with an active Pantavisor debug shell.
func scanPorts() []string {
	return scanPortsVerbose(false)
}

func scanPortsVerbose(verbose bool) []string {
	var found []string

	matches, _ := filepath.Glob("/dev/ttyUSB*")
	if g, err := filepath.Glob("/dev/ttyACM*"); err == nil {
		matches = append(matches, g...)
	}
	sort.Strings(matches)

	for _, p := range matches {
		st, err := os.Stat(p)
		if err != nil || st.Mode()&os.ModeDevice == 0 {
			continue
		}

		fd := serialProbe(p)
		if fd < 0 {
			if verbose {
				fmt.Fprintf(os.Stderr, "  %s: no debug shell\n", p)
			}
			continue
		}
		unix.Close(fd)
		found = append(found, p)
		if verbose {
			fmt.Fprintf(os.Stderr, "  %s: PROMPT FOUND\n", p)
		}
	}
	return found
}

// interactive connects to a port and provides an interactive raw session.
func interactive(port string) error {
	// Configure port with stty (handles USB-serial driver quirks)
	cmd := exec.Command("stty", "-F", port, "115200", "cs8", "-cstopb", "-parenb", "-ixon", "-icrnl", "-ocrnl", "onlcr")
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure port %s: %w", port, err)
	}

	f, err := os.OpenFile(port, os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", port, err)
	}
	defer f.Close()

	// Put local terminal in raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("cannot put terminal in raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Set serial port termios to raw (after stty, for safety); keep the prior
	// settings so we can restore them before closing the port.
	var prevTermios *unix.Termios
	if t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS); err == nil {
		prevTermios = t
		raw := *t
		raw.Iflag &^= unix.IXON | unix.IXOFF | unix.IXANY | unix.ICRNL
		raw.Oflag &^= unix.OPOST | unix.OCRNL | unix.ONLCR
		raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
		raw.Cc[unix.VMIN] = 1
		raw.Cc[unix.VTIME] = 0
		unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, &raw)
	}

	fmt.Fprintf(os.Stderr, "Connected to %s (115200 8N1, raw)\n", port)
	fmt.Fprintf(os.Stderr, "Type Ctrl+A then Ctrl+X to exit\n\n")

	// Watch for Ctrl+A then Ctrl+X escape sequence
	type escState int
	const (escIdle escState = iota; escPending)
	var es escState
	var exitReq = make(chan struct{}, 1)

	var wg sync.WaitGroup

	// Stdin → serial. Not tracked by wg: os.Stdin.Read can't be reliably
	// unblocked on exit, so we abandon this goroutine and let process teardown
	// reap it. ponytail: abandoned goroutine, fine since the process exits next.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n <= 0 && err != nil {
				break
			}
			for i := range n {
				b := buf[i]
				if es == escPending {
					es = escIdle
					if b == exitKey {
						exitReq <- struct{}{}
						return
					}
					f.Write([]byte{escapeKey})
				}
				if b == escapeKey {
					es = escPending
					continue
				}
				f.Write([]byte{b})
			}
		}
	}()

	// Serial → terminal
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// Don't catch SIGINT: in raw mode Ctrl+C is delivered as a 0x03 byte on
	// stdin and forwarded to the shell to cancel the remote process. Exit only
	// via Ctrl+A Ctrl+X (exitReq) or an external SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	select {
	case <-sig:
	case <-exitReq:
	}

	// Restore the port's prior termios, then close to unblock the read goroutine.
	if prevTermios != nil {
		unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, prevTermios)
	}
	f.Close()
	wg.Wait()
	fmt.Fprintln(os.Stderr)
	return nil
}

// resolvePort returns the port to use: explicit port > default /dev/ttyUSB0 > error.
func resolvePort(port string) (string, error) {
	if port != "" {
		if _, err := os.Stat(port); err != nil {
			return "", fmt.Errorf("port %s not found: %w", port, err)
		}
		return port, nil
	}
	// Default to /dev/ttyUSB0
	port = "/dev/ttyUSB0"
	if fd := serialProbe(port); fd >= 0 {
		unix.Close(fd)
		return port, nil
	}
	// Fallback: scan all ports
	ports := scanPorts()
	if len(ports) == 0 {
		return "", fmt.Errorf("no Pantavisor device found on /dev/ttyUSB0\n"+
			"  Use: pvr device tty -d PORT ...\n"+
			"  Or:  pvr device tty --verbose probe")
	}
	if len(ports) == 1 {
		return ports[0], nil
	}

	fmt.Println("Found Pantavisor devices:")
	for i, p := range ports {
		fmt.Printf("  [%d] %s\n", i+1, p)
	}
	fmt.Print("Select port (number or path): ")
	choice, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	// Strip "tty:" prefix if present
	if n := strings.Index(choice, ":"); n >= 0 {
		choice = choice[:n]
	}
	// Try as number
	idx := 0
	if len(choice) > 0 && choice[0] >= '1' && choice[0] <= '9' {
		for _, d := range choice {
			if d >= '0' && d <= '9' {
				idx = idx*10 + int(d-'0')
			}
		}
		if idx >= 1 && idx <= len(ports) {
			choice = ports[idx-1]
		}
	}
	return choice, nil
}

func CommandTty() cli.Command {
	return cli.Command{
		Name:      "tty",
		Usage:     "Connect to a Pantavisor device over USB serial console",
		ArgsUsage: "[options] <subcommand> [args]",
		Description: `Interact with a Pantavisor device via its USB serial console.

All commands use the port specified with -d (defaults to /dev/ttyUSB0).

SUBCOMMANDS:
  shell               Interactive serial console (default when no subcommand)
  run CMD...          Execute commands on the device
  ps                  List containers (lxc-ls + pvcontrol ls)
  log [PLAT [N]]      Tail device logs (default: pantavisor, 30 lines)
  grep PATTERN        Search device logs for PATTERN
  probe               Scan /dev/ttyUSB* and /dev/ttyACM* ports
  help                Show this help

Exit interactive shell: Ctrl+A then Ctrl+X`,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "device, d",
				Usage: "Serial port path (e.g. /dev/ttyUSB0)",
			},
			cli.BoolFlag{
				Name:  "verbose, v",
				Usage: "show verbose probe output",
			},
		},
		Action: func(c *cli.Context) error {
			port := c.String("d")
			args := c.Args()
			sub := ""
			if len(args) > 0 {
				sub = args[0]
			}
			var afterSub []string
			if len(args) > 1 {
				afterSub = args[1:]
			}

			// No subcommand or "shell" → interactive
			if sub == "" || sub == "shell" {
				port, err := resolvePort(port)
				if err != nil {
					return err
				}
				return interactive(port)
			}

			// Resolve port for all other subcommands
			if port == "" {
				p, err := resolvePort("")
				if err != nil {
					return err
				}
				port = p
			}

			switch sub {
			case "run":
				if len(afterSub) == 0 {
					return fmt.Errorf("usage: pvr device tty run CMD")
				}
				cmds := []string{strings.Join(afterSub, " ")}
				return cli.NewExitError("", runCommands(port, cmds))

			case "ps":
				return cli.NewExitError("", runCommands(port, []string{"lxc-ls", "pvcontrol ls"}))

			case "log":
				plat := "pantavisor"
				lines := "30"
				if len(afterSub) >= 1 {
					if _, err := os.Stat(afterSub[0]); err == nil {
						fmt.Fprintf(os.Stderr, "pvr device tty: ignoring %q (looks like a port path; use -d for the port)\n", afterSub[0])
					} else {
						plat = afterSub[0]
					}
				}
				if len(afterSub) >= 2 {
					if _, err := strconv.Atoi(afterSub[1]); err != nil {
						return fmt.Errorf("line count must be a number, got %q", afterSub[1])
					}
					lines = afterSub[1]
				}
				var f string
				if plat == "pantavisor" {
					f = "/pv/logs/current/pantavisor/pantavisor.log"
				} else {
					f = "/pv/logs/current/" + plat + "/lxc/console.log"
				}
				return cli.NewExitError("", runCommands(port, []string{"tail -n " + lines + " " + shQuote(f)}))

			case "grep":
				if len(afterSub) == 0 {
					return fmt.Errorf("usage: pvr device tty grep PATTERN")
				}
				return cli.NewExitError("", runCommands(port, []string{"grep -rl " + shQuote(afterSub[0]) + " /pv/logs/current/"}))

			case "probe":
				ports := scanPortsVerbose(c.Bool("verbose"))
				if len(ports) == 0 {
					fmt.Println("No Pantavisor devices found.")
				} else {
					for _, p := range ports {
						fmt.Println(p)
					}
				}
				return nil

			case "help":
				return cli.NewExitError(nil, 0)

			default:
				return fmt.Errorf("unknown subcommand: %s\nUsage: pvr device tty <shell|run|ps|log|grep|probe>", sub)
			}
		},
	}
}
