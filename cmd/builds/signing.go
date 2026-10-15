// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package build

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/urfave/cli"
	"golang.org/x/term"
)

// Signing identities are the roots a Pantavisor image trusts and the keys
// builds sign with (see the Hub's /docs/guide/builds-signing). The Hub never
// returns a private key: enroll makes one here and sends only a certificate
// request; upload-key sends one the account's own CA issued.

var identityName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`)

func identityArg(c *cli.Context, verb string) (string, error) {
	if c.NArg() != 1 || !identityName.MatchString(c.Args().First()) {
		return "", fmt.Errorf("%s takes the name of one signing identity", verb)
	}
	return url.PathEscape(c.Args().First()), nil
}

func roleFlag(c *cli.Context) (string, error) {
	role := c.String("role")
	if role != "oem" && role != "platform" {
		return "", errors.New("--role is oem (signs apps) or platform (signs the BSP and the OEM configuration)")
	}
	return role, nil
}

// proof is what the Hub asks before it gives someone a key devices trust:
// PVR_SUDO_TOKEN (from /auth/mfa/reauth, for accounts without a password),
// or the account password, typed at the terminal.
func proof(c *cli.Context) (map[string]string, error) {
	if token := strings.TrimSpace(os.Getenv("PVR_SUDO_TOKEN")); token != "" {
		return map[string]string{"sudo_token": token}, nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("confirm it is you: run on a terminal to type your password, or set PVR_SUDO_TOKEN")
	}
	defer func() { _ = tty.Close() }()
	fmt.Fprint(tty, "Your Pantahub password, to confirm it is you (input hidden): ")
	raw, err := term.ReadPassword(int(tty.Fd()))
	_, _ = fmt.Fprintln(tty)
	if err != nil {
		return nil, err
	}
	return map[string]string{"password": string(raw)}, nil
}

func signingLs(c *cli.Context) error {
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodGet, "/signing/identities", nil, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

func readPEMFile(path, what string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", what, err)
	}
	if strings.Contains(string(raw), "PRIVATE KEY") {
		return "", fmt.Errorf("%s holds a private key: give the root certificate only, and keep the root's key offline", path)
	}
	return string(raw), nil
}

func signingCreate(c *cli.Context) error {
	if c.NArg() != 1 || !identityName.MatchString(c.Args().First()) {
		return errors.New("create takes one name: 3 to 48 lowercase letters, digits or '-'; it becomes the image's PV_OEM_NAME")
	}
	body := map[string]interface{}{"name": c.Args().First(), "source": "hub", "platform": c.Bool("platform")}
	if hosts := c.StringSlice("host"); len(hosts) > 0 {
		body["hosts"] = hosts
	}
	if d := c.String("description"); d != "" {
		body["description"] = d
	}
	if c.String("oem-root") != "" {
		if c.Bool("platform") {
			return errors.New("--platform makes Hub keys; with your own roots pass --platform-root")
		}
		oem, err := readPEMFile(c.String("oem-root"), "--oem-root")
		if err != nil {
			return err
		}
		platform, err := readPEMFile(c.String("platform-root"), "--platform-root")
		if err != nil {
			return err
		}
		body["source"], body["oem_root"] = "own", oem
		delete(body, "platform")
		if platform != "" {
			body["platform_root"] = platform
		}
	} else if c.String("platform-root") != "" {
		return errors.New("--platform-root needs --oem-root")
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodPost, "/signing/identities", body, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

// writeNew writes a file that must not exist yet, unless force.
func writeNew(path string, data []byte, mode os.FileMode, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, mode)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists; pass --force to replace it", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func signingTrust(c *cli.Context) error {
	name, err := identityArg(c, "trust")
	if err != nil {
		return err
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodGet, "/signing/identities/"+name+"/trust", nil, "")
	if err != nil {
		return err
	}
	var trust struct {
		CAOEM            string `json:"ca_oem_certificates"`
		CA               string `json:"ca_certificates"`
		PantavisorConfig string `json:"pantavisor_config"`
	}
	if err := jsonUnmarshal(data, &trust); err != nil {
		return err
	}
	dir := c.String("output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string]string{"ca-oem-certificates.crt": trust.CAOEM, "pantavisor.config": trust.PantavisorConfig}
	if trust.CA != "" {
		files["ca-certificates.crt"] = trust.CA
	}
	for file, content := range files {
		if err := writeNew(filepath.Join(dir, file), []byte(content), 0o644, c.Bool("force")); err != nil {
			return err
		}
	}
	return printJSON(c, data)
}

// signingEnroll makes a key here, has the Hub certify it for the identity,
// and keeps key, certificate and root where pvr sig can use them.
func signingEnroll(c *cli.Context) error {
	name, err := identityArg(c, "enroll")
	if err != nil {
		return err
	}
	role, err := roleFlag(c)
	if err != nil {
		return err
	}
	dir := c.String("dir")
	if dir == "" {
		configDir, _ := c.App.Metadata["PVR_CONFIG_DIR"].(string)
		dir = filepath.Join(configDir, "pvs", "identities", c.Args().First()+"-"+role)
	}
	paths := map[string]string{"key": filepath.Join(dir, "key.pem"), "x5c": filepath.Join(dir, "x5c.pem"), "cacerts": filepath.Join(dir, "cacerts.pem")}
	if !c.Bool("force") {
		if _, err := os.Stat(paths["key"]); err == nil {
			return fmt.Errorf("%s exists; pass --force to make a new key", paths["key"])
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: c.Args().First()}}, key)
	if err != nil {
		return err
	}
	body, err := proof(c)
	if err != nil {
		return err
	}
	body["role"] = role
	body["csr"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodPost, "/signing/identities/"+name+"/certificates", body, "")
	if err != nil {
		return err
	}
	var enrolled struct {
		Certificate string `json:"certificate"`
		Root        string `json:"root"`
	}
	if err := jsonUnmarshal(data, &enrolled); err != nil || enrolled.Certificate == "" {
		return errors.New("the Hub answered no certificate")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeNew(paths["key"], pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0o600, c.Bool("force")); err != nil {
		return err
	}
	if err := writeNew(paths["x5c"], []byte(enrolled.Certificate), 0o644, true); err != nil {
		return err
	}
	if err := writeNew(paths["cacerts"], []byte(enrolled.Root), 0o644, true); err != nil {
		return err
	}
	out, _ := json.Marshal(map[string]interface{}{
		"identity": c.Args().First(), "role": role, "files": paths,
		"sign":   fmt.Sprintf("pvr sig --key %s --x5c %s add --part <app>", paths["key"], paths["x5c"]),
		"verify": fmt.Sprintf("pvr sig --cacerts %s ls <app>", paths["cacerts"]),
	})
	return printJSON(c, out)
}

// signingUploadKey sends a key and chain the account's own CA issued, read
// from stdin like a pvs secret.
func signingUploadKey(c *cli.Context) error {
	name, err := identityArg(c, "upload-key")
	if err != nil {
		return err
	}
	role, err := roleFlag(c)
	if err != nil {
		return err
	}
	value, err := readValue(c, true)
	if err != nil {
		return err
	}
	if !strings.Contains(value, "PRIVATE KEY") || !strings.Contains(value, "CERTIFICATE") {
		return errors.New("give the private key followed by its certificate (and any intermediates) on stdin, e.g. `cat acme-oem.key acme-oem.crt | pvr signing upload-key acme --role oem`")
	}
	body, err := proof(c)
	if err != nil {
		return err
	}
	body["value"] = value
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodPost, "/signing/identities/"+name+"/keys/"+role, body, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

func signingKeyAction(action string) cli.ActionFunc {
	return func(c *cli.Context) error {
		name, err := identityArg(c, action)
		if err != nil {
			return err
		}
		role, err := roleFlag(c)
		if err != nil {
			return err
		}
		h, err := newClient(c)
		if err != nil {
			return err
		}
		data, err := h.call(http.MethodPost, "/signing/identities/"+name+"/keys/"+role+"/"+action, map[string]string{}, "")
		if err != nil {
			return err
		}
		return printJSON(c, data)
	}
}

func signingRetire(c *cli.Context) error {
	name, err := identityArg(c, "retire")
	if err != nil {
		return err
	}
	body, err := proof(c)
	if err != nil {
		return err
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodDelete, "/signing/identities/"+name, body, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

// CommandSigning manages signing identities.
func CommandSigning() cli.Command {
	role := cli.StringFlag{Name: "role", Value: "oem", Usage: "oem (signs apps) or platform (signs the BSP and the OEM configuration)"}
	force := cli.BoolFlag{Name: "force", Usage: "replace files that exist"}
	return cli.Command{Name: "signing", Usage: "manage signing identities: the roots images trust and the keys builds sign with",
		Description: "Private keys never come back from the Hub. `enroll` makes a key on this machine and has the Hub " +
			"certify it; `upload-key` sends one your own CA issued. Both, and `retire`, ask for your password " +
			"(or PVR_SUDO_TOKEN) to confirm it is you. See /docs/guide/builds-signing on the Hub.",
		Subcommands: []cli.Command{
			{Name: "ls", Usage: "list signing identities: roots, fingerprints and build keys", Action: signingLs},
			{Name: "create", ArgsUsage: "<name>", Usage: "create an identity; the Hub makes the keys, or pass --oem-root to use your own roots", Action: signingCreate, Flags: []cli.Flag{
				cli.BoolFlag{Name: "platform", Usage: "Hub keys: also a platform root and key, to sign the BSP of your images"},
				cli.StringFlag{Name: "oem-root", Usage: "your OEM root certificate (PEM file): the identity uses your own roots"},
				cli.StringFlag{Name: "platform-root", Usage: "your platform root certificate (PEM file), with --oem-root"},
				cli.StringSliceFlag{Name: "host", Usage: "source whose exports its build keys may sign, e.g. github.com/acme (repeatable; default github.com and gitlab.com)"},
				cli.StringFlag{Name: "description", Usage: "what the identity is for"},
			}},
			{Name: "trust", ArgsUsage: "<name>", Usage: "write what an image needs to trust the identity: ca-oem-certificates.crt, ca-certificates.crt, pantavisor.config", Action: signingTrust, Flags: []cli.Flag{
				cli.StringFlag{Name: "output, o", Value: ".", Usage: "directory to write to"},
				force,
			}},
			{Name: "enroll", ArgsUsage: "<name>", Usage: "make a signing key on this machine and have the Hub certify it for the identity", Action: signingEnroll, Flags: []cli.Flag{
				role,
				cli.StringFlag{Name: "dir", Usage: "where to keep key.pem, x5c.pem and cacerts.pem (default $PVR_CONFIG_DIR/pvs/identities/<name>-<role>)"},
				force,
			}},
			{Name: "upload-key", ArgsUsage: "<name>", Usage: "own roots: send a key and chain your CA issued, on stdin", Action: signingUploadKey, Flags: []cli.Flag{role}},
			{Name: "rotate", ArgsUsage: "<name>", Usage: "Hub keys: give builds a new key for the role", Action: signingKeyAction("rotate"), Flags: []cli.Flag{role}},
			{Name: "revoke", ArgsUsage: "<name>", Usage: "stop builds signing with the role's key", Action: signingKeyAction("revoke"), Flags: []cli.Flag{role}},
			{Name: "retire", ArgsUsage: "<name>", Usage: "revoke every key and destroy the identity's root keys", Action: signingRetire},
		}}
}
