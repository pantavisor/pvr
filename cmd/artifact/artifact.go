// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

func stderr(c *cli.Context) io.Writer {
	if c.App.ErrWriter != nil {
		return c.App.ErrWriter
	}
	return os.Stderr
}

func printJSON(c *cli.Context, v interface{}) error {
	enc := json.NewEncoder(c.App.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// parseMeta turns key=value flags into the entry's meta object.
func parseMeta(pairs []string) (map[string]interface{}, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	meta := map[string]interface{}{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--meta takes key=value, got %q", p)
		}
		meta[k] = v
	}
	return meta, nil
}

// publish uploads the file and registers it as an artifact of the account.
func publish(c *cli.Context, file string, e Entry) error {
	if e.Name == "" || e.Version == "" || e.Kind == "" {
		return errors.New("--kind, --name and --version are required")
	}
	sha, size, err := hashFile(file)
	if err != nil {
		return err
	}
	e.SHA256, e.Size = sha, size
	h, err := newClient(c)
	if err != nil {
		return err
	}
	// Ask first so a Hub without artifacts fails before a large upload.
	if _, err := h.list(e.Kind, e.Name); err != nil {
		return err
	}
	sent, err := h.upload(file, filepath.Base(file), sha, size, stderr(c))
	if err != nil {
		return err
	}
	if !sent {
		fmt.Fprintln(stderr(c), "the account already has this file; not uploading again")
	}
	body, status, err := h.call(http.MethodPost, "/artifacts", e)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		return errUnsupported
	}
	if status == http.StatusConflict {
		return fmt.Errorf("%s/%s@%s is already published; remove it first or use another version", e.Kind, e.Name, e.Version)
	}
	if err != nil {
		return err
	}
	var stored Entry
	if json.Unmarshal(body, &stored) != nil {
		return errors.New("the Hub answered unexpected JSON")
	}
	if c.Bool("json") {
		return printJSON(c, stored)
	}
	fmt.Fprintf(c.App.Writer, "published %s/%s@%s (%d bytes)\nid: %s\nsha256: %s\n", stored.Kind, stored.Name, stored.Version, stored.Size, stored.ID, stored.SHA256)
	return nil
}

func artifactPublish(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("publish takes one file")
	}
	meta, err := parseMeta(c.StringSlice("meta"))
	if err != nil {
		return err
	}
	return publish(c, c.Args().First(), Entry{Kind: c.String("kind"), Name: c.String("name"),
		Version: c.String("version"), Description: c.String("description"), Meta: meta})
}

func metaString(e Entry, key string) string {
	if v, ok := e.Meta[key].(string); ok {
		return v
	}
	return ""
}

func ls(c *cli.Context, kind string, columns []string) error {
	if c.NArg() != 0 {
		return errors.New("ls accepts no arguments")
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	entries, err := h.list(kind, c.String("name"))
	if err != nil {
		return err
	}
	if c.Bool("json") {
		if entries == nil {
			entries = []Entry{}
		}
		return printJSON(c, entries)
	}
	w := tabwriter.NewWriter(c.App.Writer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.ToUpper(strings.Join(append([]string{"id"}, columns...), "\t")))
	for _, e := range entries {
		row := []string{e.ID}
		for _, col := range columns {
			switch col {
			case "kind":
				row = append(row, e.Kind)
			case "name":
				row = append(row, e.Name)
			case "version":
				row = append(row, e.Version)
			case "size":
				row = append(row, fmt.Sprintf("%.1f MiB", float64(e.Size)/(1<<20)))
			case "description":
				row = append(row, e.Description)
			default:
				row = append(row, metaString(e, col))
			}
		}
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

func artifactLs(c *cli.Context) error {
	return ls(c, c.String("kind"), []string{"kind", "name", "version", "size", "description"})
}

// rm deletes the entry named by an id (kind empty) or by kind, name and version.
func rm(c *cli.Context, kind, name, version string) error {
	h, err := newClient(c)
	if err != nil {
		return err
	}
	// The list finds the entry and tells a Hub without artifacts apart.
	entries, err := h.list("", "")
	if err != nil {
		return err
	}
	id := ""
	for _, e := range entries {
		if (kind == "" && e.ID == name) || (kind != "" && e.Kind == kind && e.Name == name && e.Version == version) {
			id = e.ID
			break
		}
	}
	if id == "" {
		return fmt.Errorf("no published artifact %s", describe(kind, name, version))
	}
	if _, _, err := h.call(http.MethodDelete, "/artifacts/"+id, nil); err != nil {
		return err
	}
	fmt.Fprintf(c.App.Writer, "removed %s\n", id)
	return nil
}

func describe(kind, name, version string) string {
	if kind == "" {
		return name
	}
	return kind + "/" + name + "@" + version
}

// splitRef parses kind/name@version (kind is fixed for sdk refs).
func splitRef(ref, fixedKind string) (kind, name, version string, err error) {
	rest := ref
	if fixedKind != "" {
		kind = fixedKind
	} else {
		k, r, ok := strings.Cut(ref, "/")
		if !ok {
			return "", "", "", errors.New("give an id or kind/name@version")
		}
		kind, rest = k, r
	}
	i := strings.LastIndex(rest, "@")
	if i <= 0 || i == len(rest)-1 {
		return "", "", "", errors.New("give an id or name@version")
	}
	return kind, rest[:i], rest[i+1:], nil
}

func artifactRm(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("rm takes an id or kind/name@version")
	}
	ref := c.Args().First()
	if idPattern.MatchString(ref) {
		return rm(c, "", ref, "")
	}
	kind, name, version, err := splitRef(ref, "")
	if err != nil {
		return err
	}
	return rm(c, kind, name, version)
}

// CommandArtifact publishes files to the account and lists or removes them.
func CommandArtifact() cli.Command {
	jsonFlag := cli.BoolFlag{Name: "json", Usage: "print JSON"}
	return cli.Command{Name: "artifact", Usage: "publish files to your Hub account and manage them",
		Description: "e.g. `pvr artifact publish toolchain.sh --kind sdk --name qemu --version 1.0 --meta machine=qemux86-64`",
		Subcommands: []cli.Command{
			{Name: "publish", ArgsUsage: "<file>", Usage: "upload a file and register it under kind, name and version", Action: artifactPublish, Flags: []cli.Flag{
				cli.StringFlag{Name: "kind", Usage: "kind of artifact, such as sdk"},
				cli.StringFlag{Name: "name", Usage: "artifact name"},
				cli.StringFlag{Name: "version", Usage: "artifact version"},
				cli.StringFlag{Name: "description", Usage: "what it is"},
				cli.StringSliceFlag{Name: "meta", Usage: "key=value to store with the entry (repeatable)"},
				jsonFlag,
			}},
			{Name: "ls", Usage: "list published artifacts", Action: artifactLs, Flags: []cli.Flag{
				cli.StringFlag{Name: "kind", Usage: "only this kind"},
				cli.StringFlag{Name: "name", Usage: "only this name"},
				jsonFlag,
			}},
			{Name: "rm", ArgsUsage: "<id|kind/name@version>", Usage: "remove a published artifact", Action: artifactRm},
		}}
}
