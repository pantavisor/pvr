// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package build

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli"
	"golang.org/x/term"
)

// Account secrets are what builds fetch private sources with. Values are
// write-only: they are read from stdin or a hidden prompt, never from a flag
// (it would land in shell history), and the Hub never returns them.

var secretName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var secretID = regexp.MustCompile(`^[a-f0-9]{24}$`)

type secretInput struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	Hosts       []string `json:"hosts,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Username    string   `json:"username,omitempty"`
	Description string   `json:"description,omitempty"`
	Value       string   `json:"value,omitempty"`
}

// readValue reads a secret value from a hidden prompt on a terminal, or from
// the first line of stdin otherwise.
func readValue(c *cli.Context, multiline bool) (string, error) {
	stdin := os.Stdin
	if term.IsTerminal(int(stdin.Fd())) {
		if multiline {
			return "", errors.New("give a pvs value on stdin, e.g. `cat key.pem chain.pem | pvr secret add ...`")
		}
		stderr := c.App.ErrWriter
		if stderr == nil {
			stderr = os.Stderr
		}
		fmt.Fprint(stderr, "Secret value (input hidden): ")
		raw, err := term.ReadPassword(int(stdin.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if multiline {
		// A pvs value is a PEM bundle: all of stdin.
		raw, err := io.ReadAll(io.LimitReader(stdin, 12<<10+1))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)) + "\n", nil
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, 8<<10+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// findSecret answers the id and type of the account secret called name.
func findSecret(h *client, name string) (string, string, error) {
	data, err := h.call(http.MethodGet, "/secrets", nil, "")
	if err != nil {
		return "", "", err
	}
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := jsonUnmarshal(data, &list); err != nil {
		return "", "", err
	}
	for _, s := range list {
		if s.Name == name && secretID.MatchString(s.ID) {
			return s.ID, s.Type, nil
		}
	}
	return "", "", fmt.Errorf("the account has no secret named %q", name)
}

func secretAdd(c *cli.Context) error {
	if c.NArg() != 1 || !secretName.MatchString(c.Args().First()) {
		return errors.New("add takes one secret name: letters, digits, '.', '_' or '-'")
	}
	in := secretInput{Name: c.Args().First(), Type: c.String("type"), Hosts: c.StringSlice("host"), Refs: c.StringSlice("ref"),
		Username: c.String("username"), Description: c.String("description")}
	if in.Type != "git" && in.Type != "registry" && in.Type != "pvs" && in.Type != "config" {
		return errors.New("--type is git, registry, pvs or config")
	}
	if len(in.Hosts) == 0 {
		return errors.New("name at least one --host it may be sent to, such as github.com/acme")
	}
	value, err := readValue(c, in.Type == "pvs")
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("no secret value given on stdin")
	}
	in.Value = value
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodPost, "/secrets", in, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

func secretSet(c *cli.Context) error {
	if c.NArg() != 1 || !secretName.MatchString(c.Args().First()) {
		return errors.New("set takes the name of one secret")
	}
	in := secretInput{Hosts: c.StringSlice("host"), Refs: c.StringSlice("ref"), Username: c.String("username"), Description: c.String("description"), Name: c.String("rename")}
	if len(in.Hosts) == 0 {
		in.Hosts = nil
	}
	if len(in.Refs) == 0 {
		in.Refs = nil
	}
	if in.Hosts == nil && in.Refs == nil && in.Username == "" && in.Description == "" && in.Name == "" && !c.Bool("value") {
		return errors.New("nothing to change: pass --value, --host, --ref, --username, --description or --rename")
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	id, kind, err := findSecret(h, c.Args().First())
	if err != nil {
		return err
	}
	if c.Bool("value") {
		value, err := readValue(c, kind == "pvs")
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("no secret value given on stdin")
		}
		in.Value = value
	}
	data, err := h.call(http.MethodPut, "/secrets/"+id, in, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

func secretRm(c *cli.Context) error {
	if c.NArg() != 1 || !secretName.MatchString(c.Args().First()) {
		return errors.New("rm takes the name of one secret")
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	id, _, err := findSecret(h, c.Args().First())
	if err != nil {
		return err
	}
	if _, err := h.callNoContent(http.MethodDelete, "/secrets/"+id); err != nil {
		return err
	}
	fmt.Fprintf(c.App.Writer, "removed secret %s\n", c.Args().First())
	return nil
}

func secretLs(c *cli.Context) error {
	if c.NArg() != 0 {
		return errors.New("ls accepts no arguments")
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodGet, "/secrets", nil, "")
	if err != nil {
		return err
	}
	return printJSON(c, data)
}

// CommandSecret manages the account secrets builds fetch private sources with.
func CommandSecret() cli.Command {
	where := cli.StringSliceFlag{Name: "host", Usage: "host and optional path the secret may be sent to, e.g. github.com/acme (repeatable)"}
	refs := cli.StringSliceFlag{Name: "ref", Usage: "branch or tag, '*' for any part (e.g. main, v*); tags/<name> matches only tags and heads/<name> only branches (e.g. tags/v* for release keys); builds may use the secret for: builds of other refs or bare commits do not get it; default any (repeatable)"}
	return cli.Command{Name: "secret", Usage: "manage the account secrets builds fetch private sources and sign exports with",
		Description: "Values are write-only: give them on stdin (or at the hidden prompt), never as a flag, " +
			"e.g. `pvr secret add acme-github --type git --host github.com/acme < token.txt`. The Hub never shows them again.",
		Subcommands: []cli.Command{
			{Name: "ls", Usage: "list secrets: names, types and where each may be sent, never values", Action: secretLs},
			{Name: "add", ArgsUsage: "<name>", Usage: "add a secret; the value is read from stdin or a hidden prompt", Action: secretAdd, Flags: []cli.Flag{
				cli.StringFlag{Name: "type", Value: "git", Usage: "git (repository token), registry (container registry login), pvs (signing key: PEM private key and certificate chain) or config (one value an OEM part's configuration names, such as a device join token)"},
				where,
				refs,
				cli.StringFlag{Name: "username", Usage: "user to log in as; required for registry, optional for git"},
				cli.StringFlag{Name: "description", Usage: "what the secret is for"},
			}},
			{Name: "set", ArgsUsage: "<name>", Usage: "change a secret; --value reads a new value from stdin or a hidden prompt", Action: secretSet, Flags: []cli.Flag{
				cli.BoolFlag{Name: "value", Usage: "replace the value"},
				cli.StringSliceFlag{Name: "host", Usage: "replace where the secret may be sent (repeatable)"},
				cli.StringSliceFlag{Name: "ref", Usage: "replace the branches and tags builds may use it for (repeatable)"},
				cli.StringFlag{Name: "username", Usage: "new user name"},
				cli.StringFlag{Name: "description", Usage: "new description"},
				cli.StringFlag{Name: "rename", Usage: "new name"},
			}},
			{Name: "rm", ArgsUsage: "<name>", Usage: "delete a secret", Action: secretRm},
		}}
}
