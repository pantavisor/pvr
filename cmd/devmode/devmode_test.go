package devmode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"abc/def-1": "abc/def-1",
		"a b":       "'a b'",
		"it's":      `'it'\''s'`,
		"$(x);`y`":  "'$(x);`y`'",
		"a\nb":      "'a\nb'",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Join("pvcontrol", "devmode", "files", "put", "c", "a b"); got != "pvcontrol devmode files put c 'a b'" {
		t.Errorf("Join = %q", got)
	}
}

func TestSplitFields(t *testing.T) {
	got, err := SplitFields(`docker exec -i "my box" 'x y'\ z`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "exec", "-i", "my box", "x y z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	if _, err := SplitFields(`a "b`); err == nil {
		t.Error("expected error for open quote")
	}
}

func TestParseTarget(t *testing.T) {
	cases := map[string]Target{
		"dev.local":           {"_pv_", "dev.local", 8222},
		"dev.local:22":        {"_pv_", "dev.local", 22},
		"root@10.0.0.2":       {"root", "10.0.0.2", 8222},
		"u@10.0.0.2:2222":     {"u", "10.0.0.2", 2222},
		"[fe80::1]:99":        {"_pv_", "fe80::1", 99},
		"fe80::1":             {"_pv_", "fe80::1", 8222},
		"box.tail1234.ts.net": {"_pv_", "box.tail1234.ts.net", 8222},
	}
	for in, want := range cases {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "@h", "h:0", "h:x", "[::1"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) should fail", bad)
		}
	}
}

func TestAliasStore(t *testing.T) {
	dir := t.TempDir()
	s := NewAliasStore(filepath.Join(dir, "cfg"))
	if names, err := s.Names(); err != nil || len(names) != 0 {
		t.Fatalf("empty store: %v %v", names, err)
	}
	if err := s.Set("bench", Alias{Target: "_pv_@10.0.0.2:8222"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("ae", Alias{Exec: "docker exec -i ae"}); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Get("bench")
	if err != nil || !ok || a.Target != "_pv_@10.0.0.2:8222" {
		t.Errorf("Get bench = %v %v %v", a, ok, err)
	}
	if _, ok, _ := s.Get("nope"); ok {
		t.Error("unexpected alias")
	}
	names, _ := s.Names()
	if !reflect.DeepEqual(names, []string{"ae", "bench"}) {
		t.Errorf("names = %v", names)
	}
	if err := s.Set("bad name", Alias{}); err == nil {
		t.Error("bad alias name accepted")
	}
	if st, err := os.Stat(s.Path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", st, err)
	}
}

func TestPathMapping(t *testing.T) {
	root := filepath.Join("work", "devmode")
	c, rel, err := LocalToRemote(root, filepath.Join(root, "nginx", "etc", "nginx", "nginx.conf"))
	if err != nil || c != "nginx" || rel != "etc/nginx/nginx.conf" {
		t.Errorf("LocalToRemote = %q %q %v", c, rel, err)
	}
	if _, _, err := LocalToRemote(root, filepath.Join(root, "nginx")); err == nil {
		t.Error("container dir alone should fail")
	}
	if _, _, err := LocalToRemote(root, filepath.Join("other", "x", "y")); err == nil {
		t.Error("path outside root should fail")
	}
	p, err := RemoteToLocal(root, "nginx", "etc/x.conf")
	if err != nil || p != filepath.Join(root, "nginx", "etc", "x.conf") {
		t.Errorf("RemoteToLocal = %q %v", p, err)
	}
	for _, bad := range []string{"../x", "a/../../x", "/etc/x", "", ".", "a\\b"} {
		if _, err := RemoteToLocal(root, "nginx", bad); err == nil {
			t.Errorf("RemoteToLocal accepted %q", bad)
		}
	}
	if _, err := RemoteToLocal(root, "../evil", "x"); err == nil {
		t.Error("container escape accepted")
	}
}

func TestComputeDiff(t *testing.T) {
	m := Manifest{Container: "c", Files: []Entry{
		{Path: "etc", Type: "dir", Mode: "0755"},
		{Path: "etc/same", Type: "file", Mode: "0644", SHA256: "aa"},
		{Path: "etc/content", Type: "file", Mode: "0644", SHA256: "bb"},
		{Path: "etc/mode", Type: "file", Mode: "0644", SHA256: "cc"},
		{Path: "etc/gone", Type: "file", Mode: "0644", SHA256: "dd"},
		{Path: "etc/removed", Type: "deleted"},
		{Path: "etc/revived", Type: "deleted"},
		{Path: "bin/ln", Type: "symlink", Target: "x"},
	}}
	local := []LocalFile{
		{Path: "etc/same", Type: "file", Mode: 0o644, SHA256: "aa"},
		{Path: "etc/content", Type: "file", Mode: 0o644, SHA256: "xx"},
		{Path: "etc/mode", Type: "file", Mode: 0o755, SHA256: "cc"},
		{Path: "etc/new", Type: "file", Mode: 0o644, SHA256: "ee"},
		{Path: "etc/revived", Type: "file", Mode: 0o644, SHA256: "ff"},
		{Path: "bin/ln", Type: "symlink", Target: "x"},
	}
	d := ComputeDiff(local, m, []string{"etc/removed", "usr/lib/x"})
	if !reflect.DeepEqual(d.Added, []Change{{"etc/new", ""}, {"etc/revived", ""}}) {
		t.Errorf("added = %v", d.Added)
	}
	if !reflect.DeepEqual(d.Changed, []Change{{"etc/content", "content"}, {"etc/mode", "mode"}}) {
		t.Errorf("changed = %v", d.Changed)
	}
	if !reflect.DeepEqual(d.OnlyOnDevice, []Change{{"etc/gone", ""}}) {
		t.Errorf("only on device = %v", d.OnlyOnDevice)
	}
	// etc/removed is already deleted on the device, usr/lib/x is not
	if !reflect.DeepEqual(d.ToDelete, []Change{{"usr/lib/x", ""}}) {
		t.Errorf("to delete = %v", d.ToDelete)
	}
	if d.Unchanged != 2 {
		t.Errorf("unchanged = %d", d.Unchanged)
	}
	if d.Empty() {
		t.Error("diff should not be empty")
	}
}

func TestScanLocal(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(root, "c", filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/a.conf", "hello")
	write("etc/.a.conf.swp", "x")
	write("etc/b~", "x")
	write(".git/config", "x")
	files, err := ScanLocal(root, "c")
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v %v", files, err)
	}
	f := files[0]
	if f.Path != "etc/a.conf" || f.Mode != 0o640 || f.Size != 5 ||
		f.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("file = %+v", f)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		`{"Error":"Devmode is disabled, enable PV_DEVMODE_ALLOWED in pantavisor.config or the OEM config"}`: KindNotAllowed,
		`{"Error":"Not running a devmode revision"}`:                                                        KindNotActive,
		`{"Error":"Container has restart_policy 'system', reboot needed; use ?reboot=1"}`:                   KindRebootNeeded,
		`Error response from daemon: No such container: x`:                                                  KindUnreachable,
		`{"Error":"boom"}`: KindGeneric,
	}
	for in, want := range cases {
		if got := classify(in).Kind; got != want {
			t.Errorf("classify(%q) = %v, want %v", in, got, want)
		}
	}
	codes := map[int]bool{}
	for _, k := range []Kind{KindGeneric, KindUsage, KindUnreachable, KindNotAllowed, KindNotActive, KindRebootNeeded, KindApplyFailed} {
		if codes[k.ExitCode()] {
			t.Errorf("duplicate exit code for %v", k)
		}
		codes[k.ExitCode()] = true
	}
}

func TestParsePolicies(t *testing.T) {
	m, err := ParsePolicies([]byte(`[{"name":"a","restart_policy":"container"},{"name":"b","restart_policy":"system"}]`))
	if err != nil || m["a"] != "container" || m["b"] != "system" {
		t.Errorf("%v %v", m, err)
	}
	if ApplyAction(m["a"]) != "restart" || ApplyAction(m["b"]) != "reboot" || ApplyAction("") != "unknown" {
		t.Error("ApplyAction")
	}
}

func TestSSHArgs(t *testing.T) {
	s := NewSSHTransport(Target{"_pv_", "dev.local", 8222}, []string{"-o", "IdentityFile=k"})
	s.ctlDir = "/tmp/x"
	a := Join(s.baseArgs()...)
	for _, want := range []string{"ControlMaster=auto", "ControlPersist=60", "ControlPath=/tmp/x/%C", "-p 8222", "-l _pv_", "IdentityFile=k"} {
		if !strings.Contains(a, want) {
			t.Errorf("ssh args miss %q: %s", want, a)
		}
	}
	if !strings.HasSuffix(a, "dev.local") {
		t.Errorf("host not last: %s", a)
	}
}

func TestDiffDirs(t *testing.T) {
	m := Manifest{Files: []Entry{
		{Path: "etc", Type: "dir", Mode: "0755"},
		{Path: "etc/keep", Type: "file", Mode: "0644", SHA256: "aa"},
		{Path: "old", Type: "dir", Mode: "0755"},
		{Path: "old/x", Type: "file", Mode: "0644", SHA256: "bb"},
		{Path: "emptyok", Type: "dir", Mode: "0755"},
		{Path: "sock", Type: "special"},
	}}
	local := []LocalFile{
		{Path: "etc/keep", Type: "file", Mode: 0o644, SHA256: "aa"},
		{Path: "emptyok", Type: "dir", Mode: 0o755},
		{Path: "newdir", Type: "dir", Mode: 0o700},
		{Path: "ln", Type: "symlink", Target: "etc"},
	}
	d := ComputeDiff(local, m, nil)
	if !reflect.DeepEqual(d.Added, []Change{{"ln", ""}, {"newdir", ""}}) {
		t.Errorf("added = %v", d.Added)
	}
	// copied-up parent directories are not device-only; files are
	if !reflect.DeepEqual(d.OnlyOnDevice, []Change{{"old/x", ""}}) {
		t.Errorf("only on device = %v", d.OnlyOnDevice)
	}
	if len(d.Changed) != 0 || d.Unchanged != 2 {
		t.Errorf("changed %v unchanged %d", d.Changed, d.Unchanged)
	}
	if !under([]string{"old"}, "old/x") || under([]string{"old"}, "older") {
		t.Error("under")
	}
}

func TestScanLocalEmptyDir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "c", "var", "empty"), 0o750)
	os.MkdirAll(filepath.Join(root, "c", "etc"), 0o755)
	os.WriteFile(filepath.Join(root, "c", "etc", "f with space.conf"), []byte("x"), 0o644)
	os.Symlink("f", filepath.Join(root, "c", "etc", "ln"))
	files, err := ScanLocal(root, "c")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Type
	}
	want := map[string]string{"var/empty": "dir", "etc/f with space.conf": "file", "etc/ln": "symlink"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

type recTransport struct{ cmds []string }

func (r *recTransport) Run(_ context.Context, c string, _ io.Reader, _, _ io.Writer) (int, error) {
	r.cmds = append(r.cmds, c)
	return 0, nil
}
func (r *recTransport) Close() error { return nil }

func TestRemoteCommands(t *testing.T) {
	rt := &recTransport{}
	c := &Client{T: rt}
	ctx := context.Background()
	c.Put(ctx, "nginx", "etc/a b's.conf", strings.NewReader("x"), PutOpts{Mode: 0o644})
	c.Put(ctx, "nginx", "var/empty", nil, PutOpts{Mode: 0o750, Type: "dir"})
	c.Put(ctx, "nginx", "bin/ln", nil, PutOpts{Type: "symlink", Target: "../x y"})
	c.Remove(ctx, "nginx", "old", true)
	c.Remove(ctx, "nginx", "f", false)
	c.Apply(ctx, "nginx", true)
	want := []string{
		`pvcontrol devmode files put nginx 'etc/a b'\''s.conf' - --uid 0 --gid 0 --mode 0644`,
		`pvcontrol devmode files put nginx var/empty --type dir --uid 0 --gid 0 --mode 0750`,
		`pvcontrol devmode files put nginx bin/ln --type symlink --target '../x y' --uid 0 --gid 0`,
		`pvcontrol devmode files rm nginx old --recursive`,
		`pvcontrol devmode files rm nginx f`,
		`pvcontrol devmode apply nginx --reboot`,
	}
	if !reflect.DeepEqual(rt.cmds, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(rt.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestClassifyMgmt(t *testing.T) {
	if classify(`{"Error":"Request not sent from a mgmt platform"}`).Kind != KindNotAllowed {
		t.Error("mgmt refusal should map to not_allowed")
	}
}
