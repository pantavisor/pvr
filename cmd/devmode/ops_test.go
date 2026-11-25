package devmode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeDev answers pvcontrol calls from canned data and records them.
type fakeDev struct {
	cmds      []string
	manifests map[string]string
	policies  string
	notFound  map[string]bool // paths whose rm answers Not found
	// ls, when set, answers `containers ls`; n counts the calls so far
	ls      func(n int) string
	lsCalls int
	// tars answers `devmode files get <container> [path] --tar`; a container
	// named in noTar answers like a pvcontrol that does not know --tar
	tars  map[string][]byte
	noTar bool
}

func (f *fakeDev) Run(_ context.Context, c string, _ io.Reader, out, errw io.Writer) (int, error) {
	f.cmds = append(f.cmds, c)
	switch {
	case c == "pvcontrol devmode status":
		io.WriteString(out, `{"enabled":true,"active":true,"ready":true,"revision":"devmode/r/1","base":"1","containers":[{"name":"a"},{"name":"b"}]}`)
	case c == "pvcontrol containers ls":
		f.lsCalls++
		if f.ls != nil {
			io.WriteString(out, f.ls(f.lsCalls))
		} else {
			io.WriteString(out, f.policies)
		}
	case strings.HasPrefix(c, "pvcontrol devmode files ls "):
		n := strings.TrimPrefix(c, "pvcontrol devmode files ls ")
		m, ok := f.manifests[n]
		if !ok {
			m = `{"container":"` + n + `","files":[]}`
		}
		io.WriteString(out, m)
	case strings.HasPrefix(c, "pvcontrol devmode files get ") && strings.HasSuffix(c, " --tar"):
		if f.noTar {
			io.WriteString(errw, "unknown option --tar")
			return 1, nil
		}
		t, ok := f.tars[strings.TrimSuffix(strings.TrimPrefix(c, "pvcontrol devmode files get "), " --tar")]
		if !ok {
			io.WriteString(errw, `{"Error":"Not found"}`)
			return 255, nil
		}
		out.Write(t)
	case strings.HasPrefix(c, "pvcontrol devmode files rm "):
		for p := range f.notFound {
			if strings.Contains(c, " "+p) {
				io.WriteString(errw, `{"Error":"Not found"}`)
				return 255, nil
			}
		}
	}
	return 0, nil
}
func (f *fakeDev) Close() error { return nil }

func newEnv(t *testing.T, f *fakeDev) (*Env, *bytes.Buffer) {
	var eb bytes.Buffer
	if f.policies == "" {
		f.policies = `[{"name":"a","restart_policy":"container"},{"name":"b","restart_policy":"system"}]`
	}
	return &Env{C: &Client{T: f}, Dir: t.TempDir(), Out: io.Discard, Err: &eb, NoWait: true}, &eb
}

func put(t *testing.T, e *Env, c, p, s string) {
	full := filepath.Join(e.Dir, c, filepath.FromSlash(p))
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func idx(cmds []string, prefix string) int {
	for i, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestSyncOrderAndSummary(t *testing.T) {
	f := &fakeDev{}
	e, errb := newEnv(t, f)
	put(t, e, "a", "etc/x.conf", "1")
	put(t, e, "a", "etc/y.conf", "2")
	put(t, e, "a", DeletedListName, "# gone\nusr/share/doc\n\n")
	os.MkdirAll(filepath.Join(e.Dir, "a", "var", "empty"), 0o755)
	put(t, e, "b", "f", "1")
	// b is a system container: without --yes the run ends with reboot_needed
	res, err := e.Sync(context.Background(), nil, false)
	if ee, ok := err.(*Error); !ok || ee.Kind != KindRebootNeeded {
		t.Fatal(err)
	}
	want := "a: emptying devmode files, uploading 2 files, 1 directory, deleting 1 image file"
	if res.Containers[0].Summary != want {
		t.Errorf("summary = %q", res.Containers[0].Summary)
	}
	if !strings.Contains(errb.String(), want) || !strings.Contains(errb.String(), "b: emptying devmode files, uploading 1 file\n") {
		t.Errorf("summary not printed up front: %q", errb.String())
	}
	c := f.cmds
	// per container: reset, uploads, deleted list; apply only at the end
	order := []string{
		"pvcontrol devmode reset a",
		"pvcontrol devmode files put a etc/x.conf",
		"pvcontrol devmode files put a var/empty --type dir",
		"pvcontrol devmode files rm a usr/share/doc --recursive",
		"pvcontrol devmode reset b",
		"pvcontrol devmode files put b f",
		"pvcontrol containers ls", // the apply step starts only after all uploads
	}
	last := -1
	for _, p := range order {
		i := idx(c, p)
		if i < 0 || i < last {
			t.Fatalf("%q out of order or missing in\n%s", p, strings.Join(c, "\n"))
		}
		last = i
	}
	if idx(c, "pvcontrol devmode apply b --reboot") >= 0 {
		t.Error("rebooted without confirmation")
	}
}

func TestSyncRebootNeedsYes(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	put(t, e, "b", "f", "1")
	_, err := e.Sync(context.Background(), []string{"b"}, false)
	if ee, ok := err.(*Error); !ok || ee.Kind != KindRebootNeeded {
		t.Fatalf("err = %v", err)
	}
	e.Yes = true
	if _, err := e.Sync(context.Background(), []string{"b"}, false); err != nil {
		t.Fatal(err)
	}
	if idx(f.cmds, "pvcontrol devmode apply b --reboot") < 0 {
		t.Error("no reboot with --yes")
	}
}

func TestSyncNoApply(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	put(t, e, "a", "f", "1")
	if _, err := e.Sync(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	if idx(f.cmds, "pvcontrol devmode apply") >= 0 {
		t.Error("applied with --no-apply")
	}
}

func TestSyncValidatesBeforeTouchingDevice(t *testing.T) {
	cases := map[string]func(e *Env, t *testing.T){
		"bad list path":   func(e *Env, t *testing.T) { put(t, e, "b", DeletedListName, "../etc\n") },
		"absolute path":   func(e *Env, t *testing.T) { put(t, e, "b", DeletedListName, "/etc/x\n") },
		"file and listed": func(e *Env, t *testing.T) { put(t, e, "b", DeletedListName, "etc\n"); put(t, e, "b", "etc/x", "1") },
		"unknown dir":     func(e *Env, t *testing.T) {},
	}
	for name, setup := range cases {
		f := &fakeDev{}
		e, _ := newEnv(t, f)
		put(t, e, "a", "ok", "1") // a is fine, b is broken
		setup(e, t)
		if name == "unknown dir" {
			if _, err := e.Sync(context.Background(), []string{"a", "nope"}, false); err == nil {
				t.Errorf("%s: expected error", name)
			}
		} else if _, err := e.Sync(context.Background(), nil, false); err == nil {
			t.Errorf("%s: expected error", name)
		}
		if len(f.cmds) != 0 {
			t.Errorf("%s: device was called: %v", name, f.cmds)
		}
	}
}

func TestPushAddsOnlyAndAppliesList(t *testing.T) {
	m := `{"container":"a","files":[
	  {"path":"tmp","type":"dir","mode":"0755"},
	  {"path":"etc/old","type":"file","mode":"0644","sha256":"aa"},
	  {"path":"gone/already","type":"deleted"}]}`
	f := &fakeDev{noTar: true, manifests: map[string]string{"a": m}}
	e, _ := newEnv(t, f)
	put(t, e, "a", "etc/new", "1")
	put(t, e, "a", DeletedListName, "gone/already\nusr/doc\n")
	res, err := e.Push(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.cmds {
		if strings.Contains(c, "files rm a tmp") || strings.Contains(c, "files rm a etc/old") || strings.Contains(c, "reset") {
			t.Errorf("push removed something it must not: %s", c)
		}
	}
	if idx(f.cmds, "pvcontrol devmode files put a etc/new") < 0 {
		t.Error("new file not pushed")
	}
	if idx(f.cmds, "pvcontrol devmode files rm a usr/doc --recursive") < 0 || idx(f.cmds, "pvcontrol devmode files rm a gone/already") >= 0 {
		t.Errorf("deleted list not applied right: %v", f.cmds)
	}
	if !reflect.DeepEqual(res[0].Deleted, []string{"usr/doc"}) {
		t.Errorf("deleted = %v", res[0].Deleted)
	}
}

func TestPushListedPathNotFoundIsFine(t *testing.T) {
	f := &fakeDev{notFound: map[string]bool{"usr/doc": true}}
	e, _ := newEnv(t, f)
	put(t, e, "a", DeletedListName, "usr/doc\n")
	res, err := e.Push(context.Background(), []string{"a"})
	if err != nil || len(res[0].Deleted) != 0 {
		t.Errorf("res %v err %v", res, err)
	}
}

func TestRmRecordsList(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	r, err := e.Remove(context.Background(), "a", []string{"usr/doc", "etc/motd"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Removed, []string{"usr/doc", "etc/motd"}) {
		t.Errorf("removed %v", r.Removed)
	}
	if idx(f.cmds, "pvcontrol devmode files rm a usr/doc --recursive") < 0 {
		t.Errorf("cmds %v", f.cmds)
	}
	list, _ := ReadDeletedList(e.Dir, "a")
	if !reflect.DeepEqual(list, []string{"etc/motd", "usr/doc"}) {
		t.Errorf("list %v", list)
	}
	// again: no duplicates, and a bad path is refused before any call
	e.Remove(context.Background(), "a", []string{"usr/doc"}, false)
	list, _ = ReadDeletedList(e.Dir, "a")
	if len(list) != 2 {
		t.Errorf("duplicate in list: %v", list)
	}
	n := len(f.cmds)
	if _, err := e.Remove(context.Background(), "a", []string{"../x"}, false); err == nil || len(f.cmds) != n+1 { // +1 = status
		t.Errorf("bad path: %v %v", err, f.cmds[n:])
	}
}

func TestPullWritesDeletedList(t *testing.T) {
	m := `{"container":"a","files":[
	  {"path":"etc","type":"dir","mode":"0755"},
	  {"path":"usr/doc","type":"deleted"},
	  {"path":"etc/motd","type":"deleted"},
	  {"path":"dev/null","type":"special"}]}`
	f := &fakeDev{noTar: true, manifests: map[string]string{"a": m}}
	e, _ := newEnv(t, f)
	res, err := e.Pull(context.Background(), []string{"a"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := ReadDeletedList(e.Dir, "a")
	if !reflect.DeepEqual(list, []string{"etc/motd", "usr/doc"}) {
		t.Errorf("list %v", list)
	}
	if len(res[0].Skipped) != 1 || !strings.Contains(res[0].Skipped[0].Reason, "special") {
		t.Errorf("skipped %v", res[0].Skipped)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", "usr")); err == nil {
		t.Error("created a directory for a deleted entry")
	}
	// nothing deleted on the device: the list goes away
	f.manifests["a"] = `{"container":"a","files":[]}`
	if _, err := e.Pull(context.Background(), []string{"a"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", DeletedListName)); err == nil {
		t.Error("stale deleted list kept")
	}
}

// A container with no devmode changes yet still gets its folder, to start editing in.
func TestPullCreatesTheContainerFolder(t *testing.T) {
	f := &fakeDev{noTar: true, manifests: map[string]string{"a": `{"container":"a","files":[]}`}}
	e, _ := newEnv(t, f)
	if _, err := e.Pull(context.Background(), []string{"a"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(e.Dir, "a")); err != nil || !fi.IsDir() {
		t.Fatalf("no folder: %v", err)
	}
	if _, err := e.Pull(context.Background(), []string{"../x"}, nil, false); err == nil {
		t.Error("a bad container name was accepted")
	}
}

func TestDeletedListNeverUploaded(t *testing.T) {
	e, _ := newEnv(t, &fakeDev{})
	put(t, e, "a", DeletedListName, "x\n")
	put(t, e, "a", "f", "1")
	files, _ := ScanLocal(e.Dir, "a")
	if len(files) != 1 || files[0].Path != "f" {
		t.Errorf("files %v", files)
	}
	if _, err := ParseDeletedList("a\n..\n"); err == nil {
		t.Error("bad list accepted")
	}
}

func TestStatusLabels(t *testing.T) {
	m := `{"container":"a","files":[
	  {"path":"etc/x","type":"file","mode":"0644","sha256":"aa"},
	  {"path":"gone","type":"deleted"}]}`
	f := &fakeDev{noTar: true, manifests: map[string]string{"a": m}}
	e, _ := newEnv(t, f)
	put(t, e, "a", "new", "1")
	put(t, e, "a", DeletedListName, "gone\nusr/doc\n")
	r, err := e.Status(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	e.printStatus(&b, r)
	out := b.String()
	for _, want := range []string{"added    new", "only on device  etc/x (sync removes it)", "to delete       usr/doc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "gone") || strings.Contains(out, "  deleted") {
		t.Errorf("already-deleted path or the bare word deleted shown:\n%s", out)
	}
}

func row(name, status, pid string, restarts int) string {
	return fmt.Sprintf(`{"name":"%s","status":"%s","restart_policy":"container","pid":%s,"started_at":"t%s","restarts":%d}`, name, status, pid, pid, restarts)
}

func waitEnv(t *testing.T, f *fakeDev, timeout time.Duration) *Env {
	e, _ := newEnv(t, f)
	e.NoWait = false
	e.Poll = time.Millisecond
	e.WaitTimeout = timeout
	return e
}

func TestApplyWaitsForRestart(t *testing.T) {
	var applyAt int
	f := &fakeDev{}
	f.ls = func(n int) string {
		// calls: 1 policies, 2 pre-wait, then the restart goes STOPPING..STARTED
		seq := []string{"STARTED", "STARTED", "STOPPING", "INSTALLED", "STARTING", "STARTED"}
		i := n - 1
		if i >= len(seq) {
			i = len(seq) - 1
		}
		pid := "10"
		if i == len(seq)-1 {
			pid = "11"
		}
		return "[" + row("a", seq[i], pid, 0) + "]"
	}
	e := waitEnv(t, f, 5*time.Second)
	res, err := e.Apply(context.Background(), []string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	applyAt = idx(f.cmds, "pvcontrol devmode apply a")
	if applyAt < 0 || f.lsCalls < 6 {
		t.Fatalf("apply at %d, ls calls %d", applyAt, f.lsCalls)
	}
	if len(res.Containers) != 1 || res.Containers[0].State != "STARTED" || res.Containers[0].Seconds <= 0 {
		t.Errorf("containers %+v", res.Containers)
	}
}

func TestApplyStartedAgainWithSameRunIsNotEnough(t *testing.T) {
	f := &fakeDev{}
	f.ls = func(n int) string { return "[" + row("a", "STARTED", "10", 0) + "]" } // never restarts
	e := waitEnv(t, f, 30*time.Millisecond)
	_, err := e.Apply(context.Background(), []string{"a"}, false)
	if ee, ok := err.(*Error); !ok || ee.Kind != KindWaitTimeout || ee.Kind.ExitCode() != 15 {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyWaitsForMidRestartBeforeRequest(t *testing.T) {
	f := &fakeDev{}
	f.ls = func(n int) string {
		switch {
		case n <= 3: // policies, then still restarting from an earlier apply
			return "[" + row("a", "STARTING", "10", 0) + "]"
		case n == 4:
			return "[" + row("a", "STARTED", "10", 0) + "]"
		}
		return "[" + row("a", "STARTED", "12", 1) + "]"
	}
	e := waitEnv(t, f, 5*time.Second)
	if _, err := e.Apply(context.Background(), []string{"a"}, false); err != nil {
		t.Fatal(err)
	}
	if f.lsCalls < 5 {
		t.Errorf("ls calls %d", f.lsCalls)
	}
	// the request came only after the container was STARTED (4th ls call)
	n := 0
	for _, c := range f.cmds {
		if c == "pvcontrol containers ls" {
			n++
		}
		if strings.HasPrefix(c, "pvcontrol devmode apply a") && n < 4 {
			t.Errorf("apply requested during the restart (after %d ls calls)", n)
		}
	}
}

func TestApplyNoWaitAndReboot(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f) // NoWait
	res, err := e.Apply(context.Background(), []string{"a"}, false)
	if err != nil || res.Containers[0].State != "restart_requested" {
		t.Fatalf("%v %+v", err, res)
	}
	// a reboot does not wait: ls never shows a change and it still returns
	f2 := &fakeDev{}
	e2 := waitEnv(t, f2, 20*time.Millisecond)
	e2.Yes = true
	res, err = e2.Apply(context.Background(), []string{"b"}, false)
	if err != nil || !res.Reboot || res.Containers[0].State != "rebooting" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestPushSummaryWording(t *testing.T) {
	e, _ := newEnv(t, &fakeDev{})
	var b bytes.Buffer
	e.printPush(&b, []PushResult{{Container: "a", Pushed: []string{"x"}, Unchanged: 2}, {Container: "b", Pushed: []string{"x"}, Deleted: []string{"p", "q"}}})
	out := b.String()
	if !strings.Contains(out, "a: 1 pushed, 2 unchanged") || !strings.Contains(out, "b: 1 pushed, 2 image paths deleted, 0 unchanged") {
		t.Errorf("%s", out)
	}
}
