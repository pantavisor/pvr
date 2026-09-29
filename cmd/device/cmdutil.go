//
// Copyright 2026  Pantacor Ltd.
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
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
)

// commandArgs returns the positional arguments of a command declared with
// SkipArgReorder, applying the flags found among them, so flags may follow
// the arguments (`pvr device ssh mydev --proxy --pubkey k.pub`, as an
// OpenSSH ProxyCommand writes it). urfave/cli's own reordering moves the
// argument after a boolean flag next to it, which swaps positional
// arguments (`live-logs dev --no-follow src` would read `src` as the
// device). Everything after `--` is positional. help is true when -h or
// --help was given.
func commandArgs(c *cli.Context) (args []string, help bool, err error) {
	return commandArgsUntil(c, 0)
}

// commandArgsUntil is commandArgs for a command whose arguments past the
// first positional ones are a command of their own (pvr device ssh <device>
// <target> <command...>): once positional arguments have been taken, the
// next one and everything after it are returned as they are, flags
// included, so "uname -m" or "sh -c ..." reach the device untouched.
// positional 0 parses flags everywhere.
func commandArgsUntil(c *cli.Context, positional int) (args []string, help bool, err error) {
	in := []string(c.Args())
	for i := 0; i < len(in); i++ {
		a := in[i]
		if a == "--" {
			args = append(args, in[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			if positional > 0 && len(args) >= positional {
				args = append(args, in[i:]...)
				break
			}
			args = append(args, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if k := strings.IndexByte(name, '='); k >= 0 {
			name, value, hasValue = name[:k], name[k+1:], true
		}
		if name == "help" || name == "h" {
			return nil, true, nil
		}
		f := findFlag(c.Command.Flags, name)
		if f == nil {
			return nil, false, fmt.Errorf("flag provided but not defined: %s", a)
		}
		if isBoolFlag(f) {
			if !hasValue {
				value = "true"
			}
		} else if !hasValue {
			if i+1 >= len(in) {
				return nil, false, fmt.Errorf("flag needs an argument: %s", a)
			}
			i++
			value = in[i]
		}
		for _, n := range flagNames(f) {
			if err := c.Set(n, value); err != nil {
				return nil, false, fmt.Errorf("invalid value %q for flag %s: %v", value, a, err)
			}
		}
	}
	return args, false, nil
}

func flagNames(f cli.Flag) []string {
	var names []string
	for _, n := range strings.Split(f.GetName(), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func findFlag(flags []cli.Flag, name string) cli.Flag {
	for _, f := range flags {
		for _, n := range flagNames(f) {
			if n == name {
				return f
			}
		}
	}
	return nil
}

func isBoolFlag(f cli.Flag) bool {
	switch f.(type) {
	case cli.BoolFlag, *cli.BoolFlag, cli.BoolTFlag, *cli.BoolTFlag:
		return true
	}
	return false
}

// signalContext is cancelled on SIGINT, SIGTERM or SIGHUP; received tells
// which signal did it (nil if none).
func signalContext(parent context.Context) (ctx context.Context, received func() os.Signal, stop func()) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	var mu sync.Mutex
	var got os.Signal
	done := make(chan struct{})
	go func() {
		select {
		case s := <-ch:
			mu.Lock()
			got = s
			mu.Unlock()
			cancel()
		case <-done:
		}
	}()
	received = func() os.Signal {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			cancel()
		})
	}
	return ctx, received, stop
}

// signalExitCode is the shell convention for death by a signal: 128 + n.
func signalExitCode(s os.Signal) int {
	if n, ok := s.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 130
}

// suggestDevices completes a device nick, as the other device commands do.
func suggestDevices(c *cli.Context) {
	if c.GlobalString("baseurl") != "" {
		c.App.Metadata["PVR_BASEURL"] = c.GlobalString("baseurl")
	}
	session, err := libpvr.NewSession(c.App)
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	if c.NArg() == 0 {
		return
	}
	searchTerm := c.Args()[c.NArg()-1]
	baseURL := c.App.Metadata["PVR_BASEURL"].(string)
	splits := strings.Split(searchTerm, "/")
	if len(splits) == 1 {
		session.SuggestDeviceNicks("", searchTerm, baseURL)
	} else {
		session.SuggestDeviceNicks(splits[0], searchTerm, baseURL)
	}
}

// deviceClient resolves the device argument (nick, id or pvr URL) and
// returns a Hub API client for it, authenticated with pvr's session.
func deviceClient(c *cli.Context, deviceArg string) (*libpvr.Session, *hubClient, *deviceInfo, error) {
	session, err := libpvr.NewSession(c.App)
	if err != nil {
		return nil, nil, nil, err
	}
	baseURL := c.App.Metadata["PVR_BASEURL"].(string)
	info, err := resolveDevice(session, baseURL, deviceArg)
	if err != nil {
		return nil, nil, nil, err
	}
	return session, &hubClient{baseURL: info.BaseURL, deviceID: info.DeviceID, call: sessionCaller(session)}, info, nil
}

// helpText indents a command description's lines after the first, which
// urfave/cli's help template indents by itself.
func helpText(s string) string {
	return strings.ReplaceAll(s, "\n", "\n   ")
}
