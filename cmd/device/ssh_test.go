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
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/urfave/cli"
	"golang.org/x/crypto/ssh"
)

// wsTestServer upgrades every request and hands the server side to fn.
func wsTestServer(t *testing.T, fn func(ws *websocket.Conn)) string {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		fn(ws)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestWSConnChunksWritesAndReadsAcrossMessages(t *testing.T) {
	type msg struct {
		kind int
		size int
	}
	got := make(chan []msg, 1)
	payload := make([]byte, 3*maxFrameBytes+123)
	rand.Read(payload)
	var received bytes.Buffer
	u := wsTestServer(t, func(ws *websocket.Conn) {
		var msgs []msg
		for received.Len() < len(payload) {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				break
			}
			msgs = append(msgs, msg{kind, len(data)})
			received.Write(data)
		}
		got <- msgs
		// answer in odd pieces, with a text message the client must skip
		ws.WriteMessage(websocket.BinaryMessage, []byte("hel"))
		ws.WriteMessage(websocket.TextMessage, []byte("ignored"))
		ws.WriteMessage(websocket.BinaryMessage, []byte("lo world"))
		ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "idle"), time.Now().Add(time.Second))
		ws.ReadMessage() // wait for the client's close
	})
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newWSConn(ws)
	defer c.Close()
	n, err := c.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write %d %v", n, err)
	}
	msgs := <-got
	if len(msgs) != 4 {
		t.Errorf("want 4 messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.kind != websocket.BinaryMessage || m.size > maxFrameBytes {
			t.Errorf("message %+v", m)
		}
	}
	if !bytes.Equal(received.Bytes(), payload) {
		t.Error("payload corrupted")
	}
	small := make([]byte, 4) // smaller than a message: reads continue within it
	var out bytes.Buffer
	for {
		n, err := c.Read(small)
		out.Write(small[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if out.String() != "hello world" {
		t.Errorf("read %q", out.String())
	}
	if c.EndReason() != "idle" || !c.ClosedByPeer() {
		t.Errorf("end reason %q closed %v", c.EndReason(), c.ClosedByPeer())
	}
}

func TestRelay(t *testing.T) {
	u := wsTestServer(t, func(ws *websocket.Conn) {
		// echo upper-cased until "bye", then end the session
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			ws.WriteMessage(websocket.BinaryMessage, bytes.ToUpper(data))
			if bytes.Contains(data, []byte("bye")) {
				ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closed"), time.Now().Add(time.Second))
				ws.ReadMessage()
				return
			}
		}
	})
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newWSConn(ws)
	defer c.Close()
	stdinR, stdinW := io.Pipe()
	var stdout syncBuffer
	done := make(chan error, 1)
	go func() { done <- relay(context.Background(), c, stdinR, &stdout) }()
	stdinW.Write([]byte("hello "))
	stdinW.Write([]byte("bye"))
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("relay: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not end with the session")
	}
	if stdout.String() != "HELLO BYE" {
		t.Errorf("stdout %q", stdout.String())
	}
	if c.EndReason() != "closed" {
		t.Errorf("reason %q", c.EndReason())
	}
	stdinW.Close()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Known hosts

func newHostKey(t *testing.T) ssh.Signer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestKnownHostsTOFU(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", knownHostsWebFile)
	host := knownHostName(testDeviceID)
	k1, k2 := newHostKey(t).PublicKey(), newHostKey(t).PublicKey()
	var out bytes.Buffer
	policy := func(interactive, acceptNew, replace bool, answer bool) *hostKeyPolicy {
		return &hostKeyPolicy{
			path: path, host: host, label: "mydev",
			interactive: interactive, acceptNew: acceptNew, replace: replace,
			confirm: func(q string) (bool, error) { return answer, nil },
			out:     &out,
		}
	}

	// unknown, not a terminal: refused, nothing written
	if err := policy(false, false, false, true).check(k1); err == nil || !strings.Contains(err.Error(), "--accept-new") {
		t.Errorf("non-interactive unknown key: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("nothing may be written for a refused key")
	}
	// unknown, the user says no
	if err := policy(true, false, false, false).check(k1); err == nil {
		t.Error("a key the user refused was trusted")
	}
	// unknown, the user says yes: shown and saved
	out.Reset()
	if err := policy(true, false, false, true).check(k1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ssh.FingerprintSHA256(k1)) {
		t.Errorf("fingerprint not shown: %q", out.String())
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("known hosts file: %v %v", st, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), host+" ssh-ed25519 ") {
		t.Errorf("file %q", data)
	}
	// known: accepted without asking
	p := policy(false, false, false, false)
	p.confirm = func(string) (bool, error) { t.Error("asked for a known key"); return false, nil }
	if err := p.check(k1); err != nil {
		t.Errorf("known key: %v", err)
	}
	// another device's key is kept apart; --accept-new saves without asking
	other := policy(false, true, false, false)
	other.host = knownHostName("ffffffffffffffffffffffff")
	if err := other.check(k2); err != nil {
		t.Fatal(err)
	}
	// changed: refused with a warning naming both keys, even with --accept-new
	err = policy(true, true, false, true).check(k2)
	var rej *errHostKeyRejected
	if !errors.As(err, &rej) || !strings.Contains(err.Error(), "HAS CHANGED") ||
		!strings.Contains(err.Error(), ssh.FingerprintSHA256(k1)) || !strings.Contains(err.Error(), ssh.FingerprintSHA256(k2)) ||
		!strings.Contains(err.Error(), "--replace-host-key") {
		t.Errorf("changed key: %v", err)
	}
	// --replace-host-key replaces it, keeping the other device's line
	if err := policy(false, false, true, false).check(k2); err != nil {
		t.Fatal(err)
	}
	status, _, _ := lookupHostKey(path, host, k2)
	old, _, _ := lookupHostKey(path, host, k1)
	otherStatus, _, _ := lookupHostKey(path, other.host, k2)
	if status != hostKeyKnown || old != hostKeyChanged || otherStatus != hostKeyKnown {
		t.Errorf("after replace: new %v old %v other %v", status, old, otherStatus)
	}
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "\n") != 2 {
		t.Errorf("file after replace:\n%s", data)
	}
}

func TestKnownHostsRevokedAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), knownHostsWebFile)
	host := knownHostName(testDeviceID)
	k := newHostKey(t).PublicKey()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	os.WriteFile(path, []byte("# comment\n\n@revoked "+host+" "+line+"\n"), 0600)
	if st, _, err := lookupHostKey(path, host, k); err != nil || st != hostKeyRevoked {
		t.Errorf("revoked: %v %v", st, err)
	}
	p := &hostKeyPolicy{path: path, host: host, label: "d", acceptNew: true, out: io.Discard}
	if err := p.check(k); err == nil {
		t.Error("a revoked key was accepted")
	}
}

func TestConfirmFrom(t *testing.T) {
	var out bytes.Buffer
	for in, want := range map[string]bool{"y\n": true, "YES\nrest": true, "n\n": false, "\n": false, "": false, "yes please\n": false} {
		r := strings.NewReader(in)
		got, err := confirmFrom(r, &out)("ok? ")
		if err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
		if in == "YES\nrest" {
			rest, _ := io.ReadAll(r)
			if string(rest) != "rest" {
				t.Errorf("read past the answer: left %q", rest)
			}
		}
	}
}

// Options, targets

func TestParseSSHOptions(t *testing.T) {
	o, err := parseSSHOptions([]string{"dev"}, false, false, "", false, false)
	if err != nil || o.target != "_pv_" || o.command != "" {
		t.Errorf("%+v %v", o, err)
	}
	o, err = parseSSHOptions([]string{"dev", "tty1@os", "ls", "-l"}, false, false, "", false, false)
	if err != nil || o.target != "tty1@os" || o.command != "ls -l" {
		t.Errorf("%+v %v", o, err)
	}
	if _, err := parseSSHOptions([]string{"dev", "_pv_"}, false, true, "k.pub", false, false); err != nil {
		t.Errorf("proxy: %v", err)
	}
	for name, c := range map[string]struct {
		args   []string
		list   bool
		proxy  bool
		pubkey string
	}{
		"bad target":        {args: []string{"dev", "a b"}},
		"two @":             {args: []string{"dev", "a@b@c"}},
		"long target":       {args: []string{"dev", strings.Repeat("a", 65)}},
		"list with target":  {args: []string{"dev", "os"}, list: true},
		"proxy w/o pubkey":  {args: []string{"dev"}, proxy: true},
		"pubkey w/o proxy":  {args: []string{"dev"}, pubkey: "k.pub"},
		"proxy and command": {args: []string{"dev", "_pv_", "ls"}, proxy: true, pubkey: "k.pub"},
		"list and proxy":    {args: []string{"dev"}, list: true, proxy: true, pubkey: "k.pub"},
	} {
		if _, err := parseSSHOptions(c.args, c.list, c.proxy, c.pubkey, false, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadPubkeyFile(t *testing.T) {
	dir := t.TempDir()
	k := newHostKey(t).PublicKey()
	good := filepath.Join(dir, "id_ed25519.pub")
	os.WriteFile(good, []byte(authorizedKeyLine(k)+" me@laptop\n"), 0644)
	got, err := readPubkeyFile(good)
	if err != nil || got != authorizedKeyLine(k) {
		t.Errorf("%q %v", got, err)
	}
	priv := filepath.Join(dir, "id_ed25519")
	os.WriteFile(priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n...\n"), 0600)
	if _, err := readPubkeyFile(priv); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Errorf("private key: %v", err)
	}
	rsa := filepath.Join(dir, "id_rsa.pub")
	os.WriteFile(rsa, []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQDDHr/jh2Jy4yALcK4JyWbVkPRaWmhck3IgCoeOO3z1e2dBowLh64QAM+Qb72pxekALga2oi4GvT+TlWNhzPH4V4ZFQ6V1pPEOSBbU3SHQgJcZG2lZ1hGBXeUp3Y7uK+rGDjJmAgzzTf6Hm7n8wMG1IdDL8jGELrmK8Vs93sV0P+Q== x\n"), 0644)
	if _, err := readPubkeyFile(rsa); err == nil || !strings.Contains(err.Error(), "ssh-ed25519") {
		t.Errorf("rsa key: %v", err)
	}
}

func TestContainersListing(t *testing.T) {
	raw := `[
	  {"name":"os","group":"root","status":"STARTED","status_goal":"STARTED"},
	  {"name":"pvr-sdk","group":"platform","status":"started","status_goal":"STARTED"},
	  {"name":"app","group":"app","status":"STOPPED","status_goal":"STARTED"},
	  {"name":"loose","status":"STARTED"},
	  {"name":"_pv_","group":"root"},
	  {"name":"bad name","group":"root"},
	  {"name":"os","group":"root"},
	  {"name":"net","group":"root","status":"STARTED"}
	]`
	cs, err := parseContainers([]byte(raw))
	if err != nil || len(cs) != 5 {
		t.Fatalf("%+v %v", cs, err)
	}
	var b bytes.Buffer
	printTargets(&b, cs)
	want := `TARGET   GROUP     STATUS
_pv_     (host)
os       root      STARTED
net      root      STARTED
pvr-sdk  platform  STARTED
app      app       STOPPED (goal STARTED)
loose    -         STARTED
`
	if b.String() != want {
		t.Errorf("targets:\n%s\nwant:\n%s", b.String(), want)
	}
	if cs2, err := parseContainers([]byte(`{"containers":[{"name":"x","status":"STARTED"}]}`)); err != nil || len(cs2) != 1 {
		t.Errorf("object form: %v %v", cs2, err)
	}
	if _, err := parseContainers([]byte(`"nope"`)); err == nil {
		t.Error("a string is not a listing")
	}
}

func TestSSHSocketURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.pantahub.com":   "wss://api.pantahub.com/devices/" + testDeviceID + "/ssh-sessions/s1/ws",
		"http://localhost:12365/":    "ws://localhost:12365/devices/" + testDeviceID + "/ssh-sessions/s1/ws",
		"https://example.com/api/v1": "wss://example.com/api/v1/devices/" + testDeviceID + "/ssh-sessions/s1/ws",
	} {
		h := &hubClient{baseURL: base, deviceID: testDeviceID}
		got, err := h.sshSocketURL("s1")
		if err != nil || got != want {
			t.Errorf("%s: %s %v", base, got, err)
		}
	}
}

// End to end: a fake Hub whose WebSocket leads to an in-process SSH server

type sshHub struct {
	*fakeHub
	hostKey ssh.Signer
	target  string // the SSH user the server wants

	mu        sync.Mutex
	pubkey    string
	ptyReq    bool
	winChange bool
	execs     []string
}

func newSSHHub(t *testing.T, target string) *sshHub {
	h := &sshHub{fakeHub: newFakeHub(t), hostKey: newHostKey(t), target: target}
	h.handle("POST /ssh-sessions", func(w http.ResponseWriter, r *http.Request) {
		var req sshSessionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !strings.HasPrefix(req.Pubkey, "ssh-ed25519 ") || req.Target != target {
			writeJSON(w, http.StatusBadRequest, map[string]string{"msg": "invalid target or key"})
			return
		}
		h.mu.Lock()
		h.pubkey = req.Pubkey
		h.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "ssh1", "target": req.Target})
	})
	h.handle("POST /ssh-sessions/{sid}/renew", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "ssh1"})
	})
	h.handle("POST /ssh-sessions/{sid}/ticket", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"ticket": "T1cket", "expires_at": time.Now().Add(30 * time.Second)})
	})
	h.handle("DELETE /ssh-sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	up := websocket.Upgrader{Subprotocols: []string{"ticket.T1cket"}}
	h.handle("GET /ssh-sessions/{sid}/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		offered := websocket.Subprotocols(r)
		if len(offered) != 1 || offered[0] != "ticket.T1cket" {
			http.Error(w, "bad ticket", http.StatusUnauthorized)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.serveSSH(t, newWSConn(ws), ws)
	})
	return h
}

// serveSSH runs an SSH server over conn that only lets the session's key in,
// as h.target. An exec answers "ran: <cmd>" and exit status 7; a shell (after
// a pty and a window change) prints "shell" and exits 0.
func (h *sshHub) serveSSH(t *testing.T, conn *wsConn, ws *websocket.Conn) {
	defer func() {
		ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closed"), time.Now().Add(time.Second))
		ws.Close()
	}()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(md ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			h.mu.Lock()
			want := h.pubkey
			h.mu.Unlock()
			if md.User() != h.target {
				return nil, fmt.Errorf("user %q", md.User())
			}
			if authorizedKeyLine(key) != want {
				return nil, errors.New("not the session key")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(h.hostKey)
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		exit := func(status uint32) {
			ch.SendRequest("exit-status", false, ssh.Marshal(struct{ S uint32 }{status}))
			ch.Close()
		}
		shell := false
		for req := range creqs {
			switch req.Type {
			case "exec":
				var p struct{ Cmd string }
				ssh.Unmarshal(req.Payload, &p)
				h.mu.Lock()
				h.execs = append(h.execs, p.Cmd)
				h.mu.Unlock()
				req.Reply(true, nil)
				time.Sleep(60 * time.Millisecond) // long enough for a renewal
				fmt.Fprintf(ch, "ran: %s\n", p.Cmd)
				exit(7)
			case "pty-req":
				var p struct {
					Term          string
					Width, Height uint32
					PW, PH        uint32
					Modes         string
				}
				ssh.Unmarshal(req.Payload, &p)
				h.mu.Lock()
				h.ptyReq = p.Term == "xterm-test" && p.Width == 100 && p.Height == 30
				h.mu.Unlock()
				req.Reply(true, nil)
			case "window-change":
				var p struct{ Width, Height, PW, PH uint32 }
				ssh.Unmarshal(req.Payload, &p)
				h.mu.Lock()
				h.winChange = p.Width == 120 && p.Height == 40
				h.mu.Unlock()
			case "shell":
				shell = true
				req.Reply(true, nil)
			default:
				req.Reply(false, nil)
			}
			h.mu.Lock()
			ready := shell && h.winChange
			h.mu.Unlock()
			if ready {
				fmt.Fprint(ch, "shell\n")
				exit(0)
			}
		}
	}
}

func testRunParams(h *sshHub, target string, errOut io.Writer) sshRunParams {
	return sshRunParams{
		api:    h.client(),
		target: target,
		dial: func(ctx context.Context, u, ticket string) (*websocket.Conn, error) {
			return dialSSHSocket(ctx, u, ticket, false)
		},
		renewEvery: 10 * time.Millisecond,
		errOut:     errOut,
	}
}

func TestSSHEndToEndCommand(t *testing.T) {
	h := newSSHHub(t, "os")
	khPath := filepath.Join(t.TempDir(), knownHostsWebFile)
	var stdout, stderr syncBuffer
	policy := &hostKeyPolicy{path: khPath, host: knownHostName(testDeviceID), label: "dev", acceptNew: true, out: &stderr}
	cp := sshClientParams{hostKey: policy.callback(), command: "echo hi", stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	status, reason, err := runInteractive(context.Background(), testRunParams(h, "os", &stderr), cp)
	if err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	if status != 7 {
		t.Errorf("exit status %d", status)
	}
	if stdout.String() != "ran: echo hi\n" {
		t.Errorf("stdout %q", stdout.String())
	}
	if len(h.execs) != 1 || h.execs[0] != "echo hi" {
		t.Errorf("execs %q", h.execs)
	}
	if h.count("DELETE /ssh-sessions/{sid}") != 1 {
		t.Error("the session was not stopped")
	}
	if h.count("POST /ssh-sessions/{sid}/renew") < 1 {
		t.Error("the session was not renewed")
	}
	if !normalSSHEnd(reason) {
		t.Errorf("reason %q", reason)
	}
	if st, _, _ := lookupHostKey(khPath, knownHostName(testDeviceID), h.hostKey.PublicKey()); st != hostKeyKnown {
		t.Error("host key not remembered")
	}

	// the device comes back with another host key: refused before anything runs
	h2 := newSSHHub(t, "os")
	policy2 := &hostKeyPolicy{path: khPath, host: knownHostName(testDeviceID), label: "dev", acceptNew: true, out: &stderr}
	var hkErr error
	cp2 := sshClientParams{
		hostKey: func(host string, a net.Addr, k ssh.PublicKey) error { hkErr = policy2.check(k); return hkErr },
		command: "rm -rf /", stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr,
	}
	_, _, err = runInteractive(context.Background(), testRunParams(h2, "os", &stderr), cp2)
	if err == nil || hkErr == nil || !strings.Contains(hkErr.Error(), "HAS CHANGED") {
		t.Errorf("changed host key: %v / %v", err, hkErr)
	}
	if len(h2.execs) != 0 {
		t.Error("ran a command on a device with a changed host key")
	}
	if h2.count("DELETE /ssh-sessions/{sid}") != 1 {
		t.Error("the refused session was not stopped")
	}
}

func TestSSHEndToEndShellWithPTY(t *testing.T) {
	h := newSSHHub(t, "_pv_")
	var stdout, stderr syncBuffer
	policy := &hostKeyPolicy{path: filepath.Join(t.TempDir(), knownHostsWebFile), host: knownHostName(testDeviceID), label: "dev", acceptNew: true, out: &stderr}
	resize := make(chan termSize, 1)
	resize <- termSize{120, 40}
	sessionHooks, restored := 0, 0
	cp := sshClientParams{
		hostKey: policy.callback(),
		stdin:   strings.NewReader(""),
		stdout:  &stdout,
		stderr:  &stderr,
		pty:     &ptyRequest{term: "xterm-test", width: 100, height: 30, resize: resize},
		onSession: func() func() {
			sessionHooks++
			return func() { restored++ }
		},
	}
	status, _, err := runInteractive(context.Background(), testRunParams(h, "_pv_", &stderr), cp)
	if err != nil || status != 0 {
		t.Fatalf("status %d err %v", status, err)
	}
	if stdout.String() != "shell\n" {
		t.Errorf("stdout %q", stdout.String())
	}
	if !h.ptyReq || !h.winChange {
		t.Errorf("pty %v window-change %v", h.ptyReq, h.winChange)
	}
	if sessionHooks != 1 || restored != 1 {
		t.Errorf("terminal hooks: set %d restored %d", sessionHooks, restored)
	}
}

func TestSSHProxyEndToEnd(t *testing.T) {
	// the ws side only echoes, standing in for the device
	h := newFakeHub(t)
	var pubkey string
	h.handle("POST /ssh-sessions", func(w http.ResponseWriter, r *http.Request) {
		var req sshSessionRequest
		json.NewDecoder(r.Body).Decode(&req)
		pubkey = req.Pubkey
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "ssh1", "target": req.Target})
	})
	h.handle("POST /ssh-sessions/{sid}/ticket", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"ticket": "T2"})
	})
	h.handle("DELETE /ssh-sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h.handle("POST /ssh-sessions/{sid}/renew", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusGone, map[string]string{"msg": "ended"})
	})
	up := websocket.Upgrader{Subprotocols: []string{"ticket.T2"}}
	h.handle("GET /ssh-sessions/{sid}/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			ws.WriteMessage(mt, data)
		}
	})
	stdinR, stdinW := io.Pipe()
	var stdout syncBuffer
	var stderr bytes.Buffer
	key := authorizedKeyLine(newHostKey(t).PublicKey())
	done := make(chan error, 1)
	rp := sshRunParams{
		api: h.client(), target: "_pv_", renewEvery: 5 * time.Millisecond, errOut: &stderr,
		dial: func(ctx context.Context, u, ticket string) (*websocket.Conn, error) {
			return dialSSHSocket(ctx, u, ticket, false)
		},
	}
	go func() {
		_, err := runProxy(context.Background(), rp, key, stdinR, &stdout)
		done <- err
	}()
	stdinW.Write([]byte("SSH-2.0-OpenSSH_9.9\r\n"))
	deadline := time.Now().Add(5 * time.Second)
	for stdout.String() != "SSH-2.0-OpenSSH_9.9\r\n" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stdinW.Close() // ssh is done: the proxy ends and stops the session
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("proxy: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not end when stdin closed")
	}
	if stdout.String() != "SSH-2.0-OpenSSH_9.9\r\n" {
		t.Errorf("stdout %q", stdout.String())
	}
	if pubkey != key {
		t.Errorf("session key %q, want the --pubkey one", pubkey)
	}
	if h.count("DELETE /ssh-sessions/{sid}") != 1 {
		t.Error("the session was not stopped")
	}
	if strings.Contains(stderr.String(), "renewing") {
		t.Errorf("a 410 renew stops renewing quietly: %q", stderr.String())
	}
}

func TestSSHTicketRefused(t *testing.T) {
	h := newFakeHub(t)
	h.handle("POST /ssh-sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "ssh1"})
	})
	h.handle("POST /ssh-sessions/{sid}/ticket", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"ticket": "T3"})
	})
	h.handle("GET /ssh-sessions/{sid}/ws", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	})
	h.handle("DELETE /ssh-sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var stderr bytes.Buffer
	rp := sshRunParams{api: h.client(), target: "_pv_", renewEvery: time.Hour, errOut: &stderr,
		dial: func(ctx context.Context, u, ticket string) (*websocket.Conn, error) {
			return dialSSHSocket(ctx, u, ticket, false)
		}}
	_, err := runProxy(context.Background(), rp, "ssh-ed25519 AAAA", strings.NewReader(""), io.Discard)
	var af *apiFailure
	if !errors.As(err, &af) || !strings.Contains(err.Error(), "refused the ticket") {
		t.Errorf("err %v", err)
	}
	if h.count("DELETE /ssh-sessions/{sid}") != 1 {
		t.Error("a session whose WebSocket failed must be stopped")
	}
}

func TestSSHRemoteCommandKeepsItsFlags(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"dev", "_pv_", "uname", "-m"}, []string{"dev", "_pv_", "uname", "-m"}},
		{[]string{"dev", "pvr-sdk", "sh", "-c", "exit 3"}, []string{"dev", "pvr-sdk", "sh", "-c", "exit 3"}},
		{[]string{"dev", "--accept-new", "_pv_", "ls", "-la"}, []string{"dev", "_pv_", "ls", "-la"}},
		{[]string{"dev", "_pv_", "--accept-new", "ls", "-la"}, []string{"dev", "_pv_", "ls", "-la"}},
	}
	for _, tc := range cases {
		app := cli.NewApp()
		var got []string
		cmd := CommandSSH()
		cmd.Action = func(c *cli.Context) error {
			args, _, err := commandArgsUntil(c, 2)
			if err != nil {
				return err
			}
			got = args
			return nil
		}
		app.Commands = []cli.Command{cmd}
		if err := app.Run(append([]string{"pvr", cmd.Name}, tc.in...)); err != nil {
			t.Fatalf("%v: %v", tc.in, err)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("%v: got %v want %v", tc.in, got, tc.want)
		}
	}
}
