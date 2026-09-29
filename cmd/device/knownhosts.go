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

// Host key trust on first use for `pvr device ssh`, in an OpenSSH
// known_hosts file (~/.pvr/known_hosts_web) whose host names are
// `pantahub:<device id>`: the device is reached through the Hub, never by an
// address, so its key is remembered per device.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const knownHostsWebFile = "known_hosts_web"

// knownHostName is the name a device's key is kept under.
func knownHostName(deviceID string) string { return "pantahub:" + deviceID }

type hostKeyStatus int

const (
	hostKeyUnknown hostKeyStatus = iota
	hostKeyKnown
	hostKeyChanged
	hostKeyRevoked
)

// lookupHostKey says whether key is the one known for host. For a changed
// key it also answers the keys known instead. A missing file knows nothing.
func lookupHostKey(path, host string, key ssh.PublicKey) (hostKeyStatus, []ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return hostKeyUnknown, nil, nil
	}
	if err != nil {
		return hostKeyUnknown, nil, err
	}
	var known []ssh.PublicKey
	matched := false
	for _, line := range strings.Split(string(data), "\n") {
		marker, hosts, pk, ok := parseKnownHostsLine(line)
		if !ok || !containsString(hosts, host) {
			continue
		}
		same := bytes.Equal(pk.Marshal(), key.Marshal())
		switch marker {
		case "revoked":
			if same {
				return hostKeyRevoked, nil, nil
			}
		case "":
			if same {
				matched = true
			} else {
				known = append(known, pk)
			}
		}
	}
	if matched {
		return hostKeyKnown, nil, nil
	}
	if len(known) > 0 {
		return hostKeyChanged, known, nil
	}
	return hostKeyUnknown, nil, nil
}

// parseKnownHostsLine parses one known_hosts line; comments, blank and
// unparsable lines answer ok false.
func parseKnownHostsLine(line string) (marker string, hosts []string, key ssh.PublicKey, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", nil, nil, false
	}
	marker, hosts, key, _, _, err := ssh.ParseKnownHosts([]byte(t + "\n"))
	if err != nil {
		return "", nil, nil, false
	}
	return marker, hosts, key, true
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// saveHostKey records key for host. With replace, the lines naming host are
// removed first. The file is written whole (0600, its folder 0700) and
// renamed into place.
func saveHostKey(path, host string, key ssh.PublicKey, replace bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}
		if replace {
			if marker, hosts, _, ok := parseKnownHostsLine(line); ok && marker == "" && containsString(hosts, host) {
				continue
			}
		}
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteString("\n")
		}
	}
	out.WriteString(host + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + "\n")

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// hostKeyPolicy decides whether to trust the key a device presents.
type hostKeyPolicy struct {
	path  string // the known_hosts file
	host  string // pantahub:<device id>
	label string // how to name the device to the user

	acceptNew   bool // trust an unknown key without asking
	replace     bool // trust a changed key, replacing the known one
	interactive bool // the user can be asked (stdin is a terminal)

	confirm func(question string) (bool, error)
	out     io.Writer // prompts and warnings
}

// errHostKeyRejected is a key the policy refused; the message says why.
type errHostKeyRejected struct{ msg string }

func (e *errHostKeyRejected) Error() string { return e.msg }

// check applies the policy to key.
func (p *hostKeyPolicy) check(key ssh.PublicKey) error {
	status, known, err := lookupHostKey(p.path, p.host, key)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p.path, err)
	}
	fp := ssh.FingerprintSHA256(key)
	switch status {
	case hostKeyKnown:
		return nil
	case hostKeyRevoked:
		return &errHostKeyRejected{fmt.Sprintf("the host key of %s (%s) is marked @revoked in %s", p.label, fp, p.path)}
	case hostKeyChanged:
		var fps []string
		for _, k := range known {
			fps = append(fps, ssh.FingerprintSHA256(k))
		}
		if !p.replace {
			return &errHostKeyRejected{fmt.Sprintf(`
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: THE DEVICE'S SSH HOST KEY HAS CHANGED!      @
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
Someone could be intercepting the session (the Hub carries it), or the
device was reflashed or had its SSH host key regenerated.
Device:      %s (%s)
Known key:   %s (in %s)
Offered key: %s %s
Refusing to connect. If the change is expected, run again with
--replace-host-key.`, p.label, p.host, strings.Join(fps, ", "), p.path, keyTypeName(key), fp)}
		}
		fmt.Fprintf(p.out, "pvr: replacing the known host key of %s (%s) with %s\n", p.label, strings.Join(fps, ", "), fp)
		return saveHostKey(p.path, p.host, key, true)
	}

	// unknown: trust on first use
	if !p.acceptNew {
		if !p.interactive || p.confirm == nil {
			return &errHostKeyRejected{fmt.Sprintf(
				"the host key of %s is not known (%s %s) and pvr cannot ask (stdin is not a terminal): run again with --accept-new to trust it",
				p.label, keyTypeName(key), fp)}
		}
		fmt.Fprintf(p.out, "The authenticity of device %s (%s) can't be established.\n", p.label, p.host)
		fmt.Fprintf(p.out, "%s key fingerprint is %s.\n", keyTypeName(key), fp)
		fmt.Fprintln(p.out, "The key reaches pvr through Pantahub: compare it with the device's own (console) to rule out a man in the middle.")
		ok, err := p.confirm("Trust this key and continue connecting? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			return &errHostKeyRejected{"host key not trusted"}
		}
	}
	if err := saveHostKey(p.path, p.host, key, false); err != nil {
		return fmt.Errorf("saving the host key in %s: %w", p.path, err)
	}
	fmt.Fprintf(p.out, "pvr: added the host key of %s (%s) to %s\n", p.label, fp, p.path)
	return nil
}

// callback is the policy as an ssh.HostKeyCallback.
func (p *hostKeyPolicy) callback() ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error { return p.check(key) }
}

// keyTypeName is the key type as OpenSSH prints it (ED25519, RSA, ...).
func keyTypeName(key ssh.PublicKey) string {
	switch key.Type() {
	case ssh.KeyAlgoED25519:
		return "ED25519"
	case ssh.KeyAlgoRSA:
		return "RSA"
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return "ECDSA"
	}
	return strings.ToUpper(key.Type())
}

// confirmFrom asks a yes/no question on out and reads the answer from in,
// one byte at a time so nothing meant for the shell is read ahead.
func confirmFrom(in io.Reader, out io.Writer) func(string) (bool, error) {
	return func(question string) (bool, error) {
		fmt.Fprint(out, question)
		var line []byte
		buf := make([]byte, 1)
		for {
			n, err := in.Read(buf)
			if n == 1 {
				if buf[0] == '\n' {
					break
				}
				line = append(line, buf[0])
				if len(line) > 64 {
					break
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return false, err
			}
		}
		answer := strings.ToLower(strings.TrimSpace(string(line)))
		return answer == "y" || answer == "yes", nil
	}
}
