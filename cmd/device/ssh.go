//go:build go1.22

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

// pvr device ssh: an SSH session to a device through Pantahub, as the Hub
// UI's Terminal tab opens it: the session key is generated here, the SSH
// bytes go over the session's WebSocket, and SSH runs end to end with the
// device's own SSH server. Contract: pv-mqtt-sdk docs/ssh.md.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/gorilla/websocket"
	"github.com/urfave/cli"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

const (
	listContainersCmd = "LIST_CONTAINERS"
	// hostTarget is the SSH user name that logs in to the Pantavisor host.
	hostTarget = "_pv_"
	maxListed  = 256

	exitSSHFailure = 255 // as OpenSSH, for a failure of the connection itself
)

var targetName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validSSHTarget: `_pv_`, a container name, or `<tty>@<container>`, as the
// API accepts them.
func validSSHTarget(t string) bool {
	if t == hostTarget {
		return true
	}
	parts := strings.Split(t, "@")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !targetName.MatchString(p) {
			return false
		}
	}
	return true
}

// API

type sshSessionRequest struct {
	Target string `json:"target"`
	Pubkey string `json:"pubkey"`
}

type sshSession struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	ExpiresAt time.Time `json:"expires_at"`
	Deadline  time.Time `json:"deadline"`
}

type sshTicket struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *hubClient) createSSHSession(ctx context.Context, req sshSessionRequest) (*sshSession, error) {
	var s sshSession
	err := h.doJSON(ctx, "starting the SSH session", http.MethodPost, h.deviceURL("ssh-sessions"), req, &s, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, describeSessionError(err)
	}
	if s.ID == "" {
		return nil, errors.New("starting the SSH session: the API answered no session id")
	}
	return &s, nil
}

func (h *hubClient) renewSSHSession(ctx context.Context, sid string) error {
	err := h.doJSON(ctx, "renewing the SSH session", http.MethodPost, h.deviceURL("ssh-sessions", sid, "renew"), struct{}{}, nil, http.StatusOK)
	if statusOf(err) == http.StatusGone {
		return errSessionGone
	}
	return err
}

func (h *hubClient) deleteSSHSession(ctx context.Context, sid string) error {
	return h.doJSON(ctx, "stopping the SSH session", http.MethodDelete, h.deviceURL("ssh-sessions", sid), nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound, http.StatusGone)
}

func (h *hubClient) sshTicket(ctx context.Context, sid string) (*sshTicket, error) {
	var t sshTicket
	err := h.doJSON(ctx, "getting the WebSocket ticket", http.MethodPost, h.deviceURL("ssh-sessions", sid, "ticket"), struct{}{}, &t, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if t.Ticket == "" {
		return nil, errors.New("getting the WebSocket ticket: the API answered no ticket")
	}
	return &t, nil
}

// sshSocketURL is the session's WebSocket: the API URL with its scheme
// turned into ws: or wss:.
func (h *hubClient) sshSocketURL(sid string) (string, error) {
	u, err := url.Parse(h.deviceURL("ssh-sessions", sid, "ws"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported API URL scheme %q", u.Scheme)
	}
	return u.String(), nil
}

// Tunnel: a session with its WebSocket attached

type sshDialer func(ctx context.Context, u, ticket string) (*websocket.Conn, error)

type sshTunnel struct {
	api       *hubClient
	session   *sshSession
	conn      *wsConn
	stopRenew context.CancelFunc
	renewDone chan struct{}
	errOut    io.Writer
	closeOnce sync.Once
}

// openSSHTunnel starts a session for pubkey, keeps its lease renewed, gets a
// ticket and attaches the WebSocket. Whatever fails after the start stops
// the session again.
func openSSHTunnel(ctx context.Context, api *hubClient, target, pubkey string, dial sshDialer, renewEvery time.Duration, errOut io.Writer) (*sshTunnel, error) {
	sess, err := api.createSSHSession(ctx, sshSessionRequest{Target: target, Pubkey: pubkey})
	if err != nil {
		return nil, err
	}
	renewCtx, stopRenew := context.WithCancel(context.Background())
	t := &sshTunnel{api: api, session: sess, stopRenew: stopRenew, renewDone: make(chan struct{}), errOut: errOut}
	go func() {
		defer close(t.renewDone)
		renewLease(renewCtx, renewEvery, func(c context.Context) error {
			return api.renewSSHSession(c, sess.ID)
		}, func(err error) {
			fmt.Fprintf(errOut, "pvr: %v\r\n", err)
		})
	}()
	fail := func(err error) (*sshTunnel, error) {
		t.Close()
		return nil, err
	}
	ticket, err := api.sshTicket(ctx, sess.ID)
	if err != nil {
		return fail(err)
	}
	u, err := api.sshSocketURL(sess.ID)
	if err != nil {
		return fail(err)
	}
	ws, err := dial(ctx, u, ticket.Ticket)
	if err != nil {
		return fail(err)
	}
	t.conn = newWSConn(ws)
	return t, nil
}

// Close closes the WebSocket, stops renewing and stops the session.
func (t *sshTunnel) Close() {
	t.closeOnce.Do(func() {
		if t.conn != nil {
			t.conn.Close()
		}
		t.stopRenew()
		<-t.renewDone
		ctx, cancel := context.WithTimeout(context.Background(), sessionStopTimeout)
		defer cancel()
		if err := t.api.deleteSSHSession(ctx, t.session.ID); err != nil {
			fmt.Fprintf(t.errOut, "pvr: %v (the session ends with its lease)\n", err)
		}
	})
}

// SSH client

type termSize struct{ width, height int }

type ptyRequest struct {
	term          string
	width, height int
	resize        <-chan termSize
}

type sshClientParams struct {
	user    string
	signer  ssh.Signer
	hostKey ssh.HostKeyCallback
	command string // "" runs the login shell
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	pty     *ptyRequest // nil: no terminal
	// onSession, when set, runs once authenticated, right before the
	// terminal and the shell are requested (raw mode goes on here, after
	// any host key prompt); what it returns runs when the session ends.
	onSession func() func()
}

// exitUnknown: the remote side ended without an exit status.
const exitUnknown = -1

// runSSHClient runs an SSH client over conn: authenticates as p.user with
// p.signer, opens a session (with a terminal if p.pty), runs the shell or
// command and relays its I/O. It answers the remote exit status, or
// exitUnknown with the error when there is none. Cancelling ctx closes conn.
func runSSHClient(ctx context.Context, conn net.Conn, addr string, p sshClientParams) (int, error) {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            p.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(p.signer)},
		HostKeyCallback: p.hostKey,
	})
	if err != nil {
		return exitUnknown, err
	}
	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()
	s, err := client.NewSession()
	if err != nil {
		return exitUnknown, err
	}
	defer s.Close()
	s.Stdin, s.Stdout, s.Stderr = p.stdin, p.stdout, p.stderr
	if p.onSession != nil {
		if restore := p.onSession(); restore != nil {
			defer restore()
		}
	}
	done := make(chan struct{})
	defer close(done)
	if p.pty != nil {
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}
		if err := s.RequestPty(p.pty.term, p.pty.height, p.pty.width, modes); err != nil {
			return exitUnknown, fmt.Errorf("requesting a terminal: %w", err)
		}
		if p.pty.resize != nil {
			go func() {
				for {
					select {
					case <-done:
						return
					case sz := <-p.pty.resize:
						_ = s.WindowChange(sz.height, sz.width)
					}
				}
			}()
		}
	}
	if p.command != "" {
		err = s.Start(p.command)
	} else {
		err = s.Shell()
	}
	if err != nil {
		return exitUnknown, err
	}
	err = s.Wait()
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitStatus(), nil
	case errors.As(err, &missing):
		return exitUnknown, nil
	}
	return exitUnknown, err
}

// newSessionKey is the session's key pair: ed25519, kept in memory only.
func newSessionKey() (ssh.Signer, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return signer, authorizedKeyLine(sshPub), nil
}

// authorizedKeyLine is `ssh-ed25519 AAAA...`, as the Hub takes it.
func authorizedKeyLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// readPubkeyFile reads the public key for --proxy: the one the user's ssh
// client offers. The Hub only takes ssh-ed25519 keys.
func readPubkeyFile(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("--pubkey: %w", err)
	}
	if strings.Contains(string(data), "PRIVATE KEY") {
		return "", fmt.Errorf("--pubkey: %s is a private key; give the public key (.pub)", path)
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return "", fmt.Errorf("--pubkey: %s: %v", path, err)
	}
	if pk.Type() != ssh.KeyAlgoED25519 {
		return "", fmt.Errorf("--pubkey: %s is a %s key; the Hub only takes ssh-ed25519 keys", path, pk.Type())
	}
	return authorizedKeyLine(pk), nil
}

// sshEndReasons: why a session ended (docs/ssh.md), for people.
var sshEndReasons = map[string]string{
	"closed":     "the SSH connection ended",
	"stopped":    "the session was stopped",
	"expired":    "the session's lease ran out (renewals failed)",
	"idle":       "no traffic for 15 minutes",
	"deadline":   "the session reached its 60 minute limit",
	"busy":       "the device already runs as many SSH sessions as it allows",
	"invalid":    "the device refused the session",
	"lost frame": "a frame was lost on the way: SSH cannot survive a lost byte",
	"overflow":   "more data than the relay could queue",
}

// normalSSHEnd: an end that is no failure.
func normalSSHEnd(reason string) bool {
	return reason == "" || reason == "closed" || reason == "stopped"
}

func describeSSHEnd(reason string) string {
	if d, ok := sshEndReasons[reason]; ok {
		return d
	}
	for key, d := range sshEndReasons {
		if strings.HasPrefix(reason, key+":") {
			return d + " (" + shownText(reason) + ")"
		}
	}
	return shownText(reason)
}

// Running

type sshRunParams struct {
	api        *hubClient
	target     string
	dial       sshDialer
	renewEvery time.Duration
	errOut     io.Writer
}

// runInteractive connects the SSH client, as target, with a fresh session
// key. It answers the remote exit status (exitUnknown if none) and the
// session's end reason.
func runInteractive(ctx context.Context, rp sshRunParams, cp sshClientParams) (int, string, error) {
	signer, pubkey, err := newSessionKey()
	if err != nil {
		return exitUnknown, "", err
	}
	cp.signer = signer
	cp.user = rp.target
	t, err := openSSHTunnel(ctx, rp.api, rp.target, pubkey, rp.dial, rp.renewEvery, rp.errOut)
	if err != nil {
		return exitUnknown, "", &apiFailure{err}
	}
	defer t.Close()
	status, err := runSSHClient(ctx, t.conn, knownHostName(rp.api.deviceID), cp)
	t.conn.Close()
	return status, t.conn.EndReason(), err
}

// runProxy relays stdin and stdout to the session's WebSocket, for an
// OpenSSH client running pvr as its ProxyCommand. It returns when either
// side closes, answering the session's end reason.
func runProxy(ctx context.Context, rp sshRunParams, pubkey string, stdin io.Reader, stdout io.Writer) (string, error) {
	t, err := openSSHTunnel(ctx, rp.api, rp.target, pubkey, rp.dial, rp.renewEvery, rp.errOut)
	if err != nil {
		return "", &apiFailure{err}
	}
	defer t.Close()
	err = relay(ctx, t.conn, stdin, stdout)
	t.conn.Close()
	return t.conn.EndReason(), err
}

// relay copies stdin to conn and conn to stdout until either ends.
func relay(ctx context.Context, conn io.ReadWriter, stdin io.Reader, stdout io.Writer) error {
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, stdin)
		done <- err
	}()
	go func() {
		_, err := io.Copy(stdout, conn)
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// apiFailure marks an error from the Hub API (exit status 4), as opposed
// to one of the SSH connection.
type apiFailure struct{ err error }

func (e *apiFailure) Error() string { return e.err.Error() }
func (e *apiFailure) Unwrap() error { return e.err }

// Targets (LIST_CONTAINERS)

type sshContainer struct {
	Name       string
	Group      string
	Status     string
	StatusGoal string
}

// parseContainers reads the output of LIST_CONTAINERS: pv-ctrl's array of
// {name, group, status, status_goal, ...} (or an object with a
// `containers` array). Names the API would refuse as a target are left out.
func parseContainers(raw []byte) ([]sshContainer, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		var obj struct {
			Containers []json.RawMessage `json:"containers"`
		}
		if err2 := json.Unmarshal(raw, &obj); err2 != nil || obj.Containers == nil {
			return nil, fmt.Errorf("unexpected LIST_CONTAINERS output: %v", err)
		}
		list = obj.Containers
	}
	var out []sshContainer
	seen := map[string]bool{}
	for _, item := range list {
		var c struct {
			Name       string `json:"name"`
			Group      string `json:"group"`
			Status     string `json:"status"`
			StatusGoal string `json:"status_goal"`
		}
		if json.Unmarshal(item, &c) != nil || c.Name == hostTarget || !targetName.MatchString(c.Name) || seen[c.Name] {
			continue
		}
		if len(out) >= maxListed {
			break
		}
		seen[c.Name] = true
		out = append(out, sshContainer{
			Name:       c.Name,
			Group:      shownText(c.Group),
			Status:     strings.ToUpper(shownText(c.Status)),
			StatusGoal: strings.ToUpper(shownText(c.StatusGoal)),
		})
	}
	return out, nil
}

// printTargets prints the host and the containers, grouped by group in the
// device's order, containers without a group last.
func printTargets(w io.Writer, containers []sshContainer) {
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tGROUP\tSTATUS")
	fmt.Fprintf(tw, "%s\t%s\t%s\n", hostTarget, "(host)", "")
	var groups []string
	byGroup := map[string][]sshContainer{}
	for _, c := range containers {
		if _, ok := byGroup[c.Group]; !ok && c.Group != "" {
			groups = append(groups, c.Group)
		}
		byGroup[c.Group] = append(byGroup[c.Group], c)
	}
	groups = append(groups, "")
	for _, g := range groups {
		for _, c := range byGroup[g] {
			group := c.Group
			if group == "" {
				group = "-"
			}
			status := c.Status
			if c.StatusGoal != "" && c.StatusGoal != c.Status {
				status += " (goal " + c.StatusGoal + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, group, status)
		}
	}
	tw.Flush()
	for _, line := range strings.SplitAfter(buf.String(), "\n") {
		if line != "" {
			fmt.Fprintln(w, strings.TrimRight(line, " \n"))
		}
	}
}

// Command

type sshOptions struct {
	target         string
	command        string
	list           bool
	proxy          bool
	pubkeyFile     string
	acceptNew      bool
	replaceHostKey bool
}

func parseSSHOptions(args []string, list, proxy bool, pubkey string, acceptNew, replace bool) (*sshOptions, error) {
	o := &sshOptions{target: hostTarget, list: list, proxy: proxy, pubkeyFile: pubkey, acceptNew: acceptNew, replaceHostKey: replace}
	if len(args) > 1 {
		o.target = args[1]
	}
	if len(args) > 2 {
		o.command = strings.Join(args[2:], " ")
	}
	switch {
	case list && len(args) > 1:
		return nil, errors.New("--list takes no target")
	case list && proxy:
		return nil, errors.New("--list and --proxy do not go together")
	case !validSSHTarget(o.target):
		return nil, fmt.Errorf("invalid target %q: %s, a container name or <tty>@<container>", o.target, hostTarget)
	case proxy && pubkey == "":
		return nil, errors.New("--proxy needs --pubkey: the public key your ssh client will offer")
	case !proxy && pubkey != "":
		return nil, errors.New("--pubkey only goes with --proxy")
	case proxy && o.command != "":
		return nil, errors.New("--proxy relays the connection: give the command to ssh instead")
	}
	return o, nil
}

func CommandSSH() cli.Command {
	return cli.Command{
		Name:           "ssh",
		ArgsUsage:      "<NICK|ID> [TARGET [COMMAND...]]",
		Usage:          "Open an SSH session to a device through Pantahub",
		SkipArgReorder: true,
		Description: helpText(`Opens an SSH session to a device connected to Pantahub over MQTT
(pv-mqtt-sdk), as the Hub UI's Terminal tab does: SSH runs end to end with the
device's SSH server, the Hub only relays the encrypted bytes.

TARGET is the SSH user: _pv_ (the Pantavisor host, the default), a container
name, or <tty>@<container> for that container's console. --list shows them.

   pvr device ssh mydevice
   pvr device ssh mydevice os
   pvr device ssh mydevice _pv_ cat /proc/cmdline
   pvr device ssh mydevice --list

pvr generates a key pair for each session and keeps the private key in memory;
the device accepts its public key for the length of the session. The device's
host key is trusted on first use and kept in ~/.pvr/known_hosts_web (OpenSSH
format, under pantahub:<device id>): the first time pvr shows its fingerprint
and asks (or needs --accept-new when stdin is not a terminal); a changed key
is refused unless --replace-host-key.

As an OpenSSH ProxyCommand (--proxy), pvr only relays the connection and your
ssh client does SSH with your own key. The session must authorize that key,
so give its public half with --pubkey (ssh-ed25519 only):

   ssh -o ProxyCommand='pvr device ssh %h --proxy --pubkey ~/.ssh/id_ed25519.pub' _pv_@<device>

or in ~/.ssh/config (%r passes the target, i.e. the ssh user, to the Hub):

   Host mydevice
       User _pv_
       ProxyCommand pvr device ssh %h %r --proxy --pubkey ~/.ssh/id_ed25519.pub

The exit status is the remote command's or shell's when known; 255 when the SSH
connection fails, 4 when the Hub refuses the session, 2 for bad arguments.`),
		BashComplete: suggestDevices,
		Action: func(c *cli.Context) error {
			args, help, err := commandArgsUntil(c, 2)
			if err != nil {
				return cli.NewExitError(err.Error(), 2)
			}
			if help {
				return cli.ShowCommandHelp(c, c.Command.Name)
			}
			if len(args) < 1 {
				return cli.NewExitError("Device ID or Nick is required. See --help", 2)
			}
			opts, err := parseSSHOptions(args, c.Bool("list"), c.Bool("proxy"), c.String("pubkey"), c.Bool("accept-new"), c.Bool("replace-host-key"))
			if err != nil {
				return cli.NewExitError(err.Error(), 2)
			}
			var pubkey string
			if opts.proxy {
				if pubkey, err = readPubkeyFile(opts.pubkeyFile); err != nil {
					return cli.NewExitError(err.Error(), 2)
				}
			}
			session, api, _, err := deviceClient(c, args[0])
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}

			ctx, received, stop := signalContext(context.Background())
			defer stop()
			exit := func(err error, code int) error {
				if s := received(); s != nil {
					return cli.NewExitError("", signalExitCode(s))
				}
				var af *apiFailure
				if errors.As(err, &af) {
					code = 4
				}
				return cli.NewExitError("pvr: "+err.Error(), code)
			}

			if opts.list {
				raw, err := api.runCommand(ctx, listContainersCmd, nil, commandPollInterval, commandMaxWait)
				if err != nil {
					return exit(&apiFailure{err}, 4)
				}
				containers, err := parseContainers(raw)
				if err != nil {
					return exit(err, 4)
				}
				printTargets(os.Stdout, containers)
				return nil
			}

			insecure := c.GlobalBool("insecure")
			rp := sshRunParams{
				api:    api,
				target: opts.target,
				dial: func(ctx context.Context, u, ticket string) (*websocket.Conn, error) {
					return dialSSHSocket(ctx, u, ticket, insecure)
				},
				renewEvery: renewInterval,
				errOut:     os.Stderr,
			}

			if opts.proxy {
				reason, err := runProxy(ctx, rp, pubkey, os.Stdin, os.Stdout)
				if err != nil && !errors.Is(err, io.EOF) {
					return exit(err, exitSSHFailure)
				}
				if s := received(); s != nil {
					return cli.NewExitError("", signalExitCode(s))
				}
				if !normalSSHEnd(reason) {
					return cli.NewExitError("pvr: session ended: "+describeSSHEnd(reason), exitSSHFailure)
				}
				return nil
			}

			stdinFd := int(os.Stdin.Fd())
			interactive := term.IsTerminal(stdinFd)
			policy := &hostKeyPolicy{
				path:        filepath.Join(session.GetConfigDir(), knownHostsWebFile),
				host:        knownHostName(api.deviceID),
				label:       args[0],
				acceptNew:   opts.acceptNew,
				replace:     opts.replaceHostKey,
				interactive: interactive,
				confirm:     confirmFrom(os.Stdin, os.Stderr),
				out:         os.Stderr,
			}
			var hostKeyErr error
			cp := sshClientParams{
				hostKey: func(host string, remote net.Addr, key ssh.PublicKey) error {
					hostKeyErr = policy.check(key)
					return hostKeyErr
				},
				command: opts.command,
				stdin:   os.Stdin,
				stdout:  os.Stdout,
				stderr:  os.Stderr,
			}
			if interactive && opts.command == "" {
				w, h, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					w, h = 80, 24
				}
				termName := os.Getenv("TERM")
				if termName == "" {
					termName = "xterm-256color"
				}
				resize := make(chan termSize, 1)
				cp.pty = &ptyRequest{term: termName, width: w, height: h, resize: resize}
				cp.onSession = func() func() {
					state, err := term.MakeRaw(stdinFd)
					stopWatch := watchWindowSize(ctx, int(os.Stdout.Fd()), func(w, h int) {
						select {
						case resize <- termSize{w, h}:
						default:
						}
					})
					return func() {
						stopWatch()
						if err == nil {
							term.Restore(stdinFd, state)
						}
					}
				}
			}

			status, reason, err := runInteractive(ctx, rp, cp)
			if s := received(); s != nil {
				return cli.NewExitError("", signalExitCode(s))
			}
			if err != nil {
				if hostKeyErr != nil {
					return cli.NewExitError("pvr: "+hostKeyErr.Error(), exitSSHFailure)
				}
				msg := err.Error()
				if reason != "" && !normalSSHEnd(reason) {
					msg += "\npvr: session ended: " + describeSSHEnd(reason)
				} else if strings.Contains(msg, "unable to authenticate") {
					msg += " (the device did not accept the session key: is its pvr-sdk.authorized_keys user-meta near the 4096 byte limit?)"
				}
				code := exitSSHFailure
				var af *apiFailure
				if errors.As(err, &af) {
					code = 4
				}
				return cli.NewExitError("pvr: "+msg, code)
			}
			if status != exitUnknown {
				if status != 0 {
					return cli.NewExitError("", status)
				}
				return nil
			}
			if !normalSSHEnd(reason) {
				return cli.NewExitError("pvr: session ended: "+describeSSHEnd(reason), exitSSHFailure)
			}
			return nil
		},
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "list, l",
				Usage: "list the targets (the host and the containers) and exit",
			},
			cli.BoolFlag{
				Name:  "proxy",
				Usage: "relay stdin/stdout to the session, for use as an OpenSSH ProxyCommand (needs --pubkey)",
			},
			cli.StringFlag{
				Name:  "pubkey",
				Usage: "with --proxy: the ssh-ed25519 public key file of the key your ssh client offers",
			},
			cli.BoolFlag{
				Name:  "accept-new",
				Usage: "trust the device's host key without asking the first time",
			},
			cli.BoolFlag{
				Name:  "replace-host-key",
				Usage: "trust a changed host key, replacing the known one",
			},
		},
	}
}
