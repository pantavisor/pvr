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

package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
)

// short curl flags that take a value as the next argv element (only when the
// short cluster is exactly "-X", not "-XPOST")
const curlShortValueFlags = "AbcdDeEFHKmoPQrtTuUwxXyYz"

func CommandCurl() cli.Command {
	return cli.Command{
		Name:      "curl",
		ArgsUsage: "[curl-options] <URL>",
		Usage:     "run curl(1) with pvr's bearer token injected",
		Description: "Thin wrapper around the system curl(1). All arguments " +
			"are passed through to curl unchanged. pvr resolves the bearer " +
			"token for the target URL's host using the same auth chain as " +
			"`pvr clone`/`get`/`whoami` (cached -> refresh -> .netrc -> " +
			"interactive OAuth) and injects it as " +
			"'Authorization: Bearer ...'. Requires curl(1) in PATH. " +
			"If you pass your own '-H Authorization:' header, pvr does not " +
			"override it.",
		SkipFlagParsing: true,
		HideHelp:        true,
		Action: func(c *cli.Context) error {
			args := []string(c.Args())

			target := findTargetURL(args)

			bearer := ""
			if target != "" && !libpvr.IsLocalDeviceURL(target) && !userSuppliedAuthHeader(args) {
				if t, _ := c.App.Metadata["PVR_AUTH"].(string); t != "" {
					// user passed --access-token / PVR_ACCESSTOKEN
					bearer = t
				} else if u, err := url.Parse(target); err == nil && u.Host != "" {
					bearer = probeBearer(c, u)
				}
			}

			return execCurl(args, bearer)
		},
	}
}

// findTargetURL walks args using a best-effort curl flag model to find the
// first positional http(s) URL. See plan for documented limits (a flag *value*
// that itself starts with http(s):// preceding the real URL can fool this).
func findTargetURL(args []string) string {
	afterDoubleDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]

		if afterDoubleDash {
			if isURLArg(a) {
				return a
			}
			continue
		}
		if a == "--" {
			afterDoubleDash = true
			continue
		}

		if isURLArg(a) {
			return a
		}

		// long flag
		if strings.HasPrefix(a, "--") {
			if strings.Contains(a, "=") {
				continue // value embedded
			}
			// If the next arg looks like a value (not a flag, not a URL),
			// assume this long flag took it.
			if i+1 < len(args) {
				next := args[i+1]
				if !strings.HasPrefix(next, "-") && !isURLArg(next) {
					i++
				}
			}
			continue
		}

		// short flag cluster
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			last := a[len(a)-1]
			if len(a) == 2 && strings.IndexByte(curlShortValueFlags, last) >= 0 {
				// `-X` style — next arg is the value
				if i+1 < len(args) {
					i++
				}
			}
			// `-XPOST` (inline value) or boolean cluster — no extra arg
			continue
		}

		// non-flag, non-URL positional (rare for curl) — ignore
	}
	return ""
}

func isURLArg(a string) bool {
	return strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://")
}

// userSuppliedAuthHeader returns true if the user already put an
// Authorization header in their curl args, in any of curl's accepted forms.
func userSuppliedAuthHeader(args []string) bool {
	hasAuthPrefix := func(s string) bool {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "authorization:")
	}
	for i, a := range args {
		switch {
		case a == "-H" || a == "--header":
			if i+1 < len(args) && hasAuthPrefix(args[i+1]) {
				return true
			}
		case strings.HasPrefix(a, "-H"):
			if hasAuthPrefix(a[2:]) {
				return true
			}
		case strings.HasPrefix(a, "--header="):
			if hasAuthPrefix(a[len("--header="):]) {
				return true
			}
		}
	}
	return false
}

// probeBearer runs the standard pvr auth chain against the target host's
// /auth/auth_status endpoint and returns the bearer that ends up in app
// metadata. Errors are non-fatal: we just fall back to "no auth injected"
// and let curl proceed unauthenticated — same as plain curl would.
func probeBearer(c *cli.Context, target *url.URL) string {
	session, err := libpvr.NewSession(c.App)
	if err != nil {
		return ""
	}

	probeURL := target.Scheme + "://" + target.Host + "/auth/auth_status"
	_, _ = session.DoAuthCall(true, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = probeURL
		return nil
	})

	bearer, _ := c.App.Metadata["PVR_AUTH"].(string)
	return bearer
}

func execCurl(args []string, bearer string) error {
	curlBin, err := exec.LookPath("curl")
	if err != nil {
		return cli.NewExitError("pvr curl: curl(1) not found in PATH", 127)
	}

	final := make([]string, 0, len(args)+2)
	if bearer != "" {
		final = append(final, "-H", "Authorization: Bearer "+bearer)
	}
	final = append(final, args...)

	c := exec.Command(curlBin, final...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return cli.NewExitError(fmt.Errorf("pvr curl: exec failed: %w", err), 1)
	}
	return nil
}
