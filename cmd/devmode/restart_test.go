package devmode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRmRemovesLocalCopyAndSyncSucceeds(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	put(t, e, "a", "etc/version", "1")
	put(t, e, "a", "etc/keep", "2")
	if _, err := e.Remove(context.Background(), "a", []string{"etc/version"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.Dir, "a", "etc", "version")); !os.IsNotExist(err) {
		t.Errorf("local copy is still there: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(e.Dir, "a", "etc", "keep")); err != nil {
		t.Errorf("another file went too: %v", err)
	}
	if _, err := e.Sync(context.Background(), []string{"a"}, false); err != nil {
		t.Fatalf("sync after rm: %v", err)
	}
	// a file that really is in both places is still refused
	put(t, e, "a", "etc/version", "again")
	if _, err := e.Sync(context.Background(), []string{"a"}, false); err == nil {
		t.Error("sync accepted a path that is local and on the deleted list")
	}
}

func TestRmDropsIndexEntry(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	put(t, e, "a", "version", "1")
	ix := newIndex()
	l, _, _ := statLocal(filepath.Join(e.Dir, "a"), "version")
	ix.set(entryOf(l, 0, 0))
	if err := ix.save(e.Dir, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Remove(context.Background(), "a", []string{"version"}, false); err != nil {
		t.Fatal(err)
	}
	ix, err := loadIndex(e.Dir, "a")
	if err != nil || ix == nil {
		t.Fatal(err)
	}
	if _, ok := ix.get("version"); ok {
		t.Error("index still has the path")
	}
	r, err := e.Push(context.Background(), []string{"a"})
	if err != nil || len(r[0].Pushed)+len(r[0].Deleted) != 0 {
		t.Errorf("push after rm: %v %+v", err, r)
	}
}

func applies(f *fakeDev) []string { return calls(f, "pvcontrol devmode apply") }

func TestRestartDefaultOnPvcontrol(t *testing.T) {
	ctx := context.Background()
	t.Run("push", func(t *testing.T) {
		f := &fakeDev{}
		e, _ := newEnv(t, f)
		put(t, e, "a", "x", "1")
		put(t, e, "b", "x", "1")
		r, err := e.Push(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "applies", applies(f), []string{"pvcontrol devmode apply a"})
		if !r[0].Restarted || r[1].Restarted || !strings.Contains(r[1].RestartNote, "system container") {
			t.Errorf("%+v", r)
		}
		var sb strings.Builder
		e.printPush(&sb, r)
		if !strings.Contains(sb.String(), "b: not restarted, it is a system container") || !strings.Contains(sb.String(), "pvr devmode apply <device> b --yes") {
			t.Errorf("%s", sb.String())
		}
	})
	t.Run("push nothing changed", func(t *testing.T) {
		f := &fakeDev{}
		e, _ := newEnv(t, f)
		put(t, e, "a", "x", "1")
		e.Push(ctx, nil)
		f.cmds = nil
		e.Push(ctx, nil)
	})
	t.Run("rm", func(t *testing.T) {
		f := &fakeDev{}
		e, _ := newEnv(t, f)
		r, err := e.Remove(ctx, "a", []string{"etc/x", "etc/y"}, false)
		if err != nil || !r.Restarted {
			t.Fatalf("%v %+v", err, r)
		}
		eq(t, "applies", applies(f), []string{"pvcontrol devmode apply a"}) // once, after both
		r, _ = e.Remove(ctx, "b", []string{"etc/x"}, false)
		if r.Restarted || r.RestartNote == "" || len(applies(f)) != 1 {
			t.Errorf("system: %+v %v", r, applies(f))
		}
	})
	t.Run("sync", func(t *testing.T) {
		f := &fakeDev{}
		e, _ := newEnv(t, f)
		put(t, e, "a", "x", "1")
		res, err := e.Sync(ctx, nil, false)
		if err != nil || !res.Containers[0].Restarted {
			t.Fatalf("%v %+v", err, res.Containers)
		}
		eq(t, "applies", applies(f), []string{"pvcontrol devmode apply a"})
	})
	t.Run("--no-restart", func(t *testing.T) {
		f := &fakeDev{}
		e, errb := newEnv(t, f)
		e.NoRestart = true
		put(t, e, "a", "x", "1")
		if _, err := e.Push(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Remove(ctx, "a", []string{"y"}, false); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Sync(ctx, nil, false); err != nil {
			t.Fatal(err)
		}
		if len(applies(f)) != 0 {
			t.Errorf("restarted: %v %s", applies(f), errb.String())
		}
	})
}

func TestResetRestartsAppContainer(t *testing.T) {
	f := &fakeDev{}
	e, _ := newEnv(t, f)
	ctx := context.Background()
	if err := e.C.Reset(e.opCtx(ctx, true), "a"); err != nil {
		t.Fatal(err)
	}
	r, err := e.afterChange(ctx, "a", true)
	if err != nil || !r.Restarted {
		t.Fatalf("%v %+v", err, r)
	}
	eq(t, "applies", applies(f), []string{"pvcontrol devmode apply a"})
}

func TestRestartDefaultOnHTTP(t *testing.T) {
	f := &fakeCGI{reply: func(call string) (int, string) {
		switch call {
		case "GET /devmode":
			return 200, `{"enabled":true,"active":true,"ready":true,"revision":"devmode/r/1","base":"1","containers":[{"name":"a"}]}`
		case "GET /containers":
			return 200, `[{"name":"a","status":"STARTED","restart_policy":"container"},{"name":"b","restart_policy":"system"}]`
		}
		return 200, "{}"
	}}
	e := &Env{C: newHTTPBackend(t, f), Dir: t.TempDir(), Out: nil, Err: &strings.Builder{}, NoWait: true}
	ctx := context.Background()
	if _, err := e.Remove(ctx, "a", []string{"etc/x"}, false); err != nil {
		t.Fatal(err)
	}
	if !contains(f.calls, "PUT /containers/a") || !strings.Contains(f.body["PUT /containers/a"], `"restart"`) {
		t.Errorf("calls: %v", f.calls)
	}
	f.calls = nil
	r, _ := e.Remove(ctx, "b", []string{"etc/x"}, false)
	if contains(f.calls, "PUT /containers/b") || r.Restarted || r.RestartNote == "" {
		t.Errorf("system: %v %+v", f.calls, r)
	}
	f.calls = nil
	e.NoRestart = true
	e.Remove(ctx, "a", []string{"etc/y"}, false)
	if contains(f.calls, "PUT /containers/a") {
		t.Errorf("--no-restart restarted: %v", f.calls)
	}
}

func hubEnv(t *testing.T) (*fakeHub, *Env) {
	f, h, _ := hubFixture(t)
	f.reply("get_devmode_context", map[string]interface{}{"connected": true, "devmode": true, "devmode_base": "5", "devmode_on_device": "devmode/r/5"})
	f.reply("container_status", map[string]interface{}{"containers": []interface{}{
		map[string]interface{}{"name": "a", "restart_policy": "container"}, map[string]interface{}{"name": "b", "restart_policy": "system"}}})
	return f, &Env{C: h, Dir: t.TempDir(), Out: nil, Err: &strings.Builder{}, NoWait: true}
}

func callsOf(f *fakeHub, tool string) []map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]interface{}
	for _, c := range f.calls {
		if c.Tool == tool {
			out = append(out, c.Args)
		}
	}
	return out
}

func TestHubRestartIsLeftToTheDevice(t *testing.T) {
	f, e := hubEnv(t)
	ctx := context.Background()
	f.reply("apply_changes", map[string]interface{}{"restarted": true})
	r, err := e.Remove(ctx, "a", []string{"x", "y"}, false)
	if err != nil || !r.Restarted {
		t.Fatalf("%v %+v", err, r)
	}
	cs := callsOf(f, "apply_changes")
	if len(cs) != 2 {
		t.Fatalf("calls %v", cs)
	}
	// only the last call may restart: the first says restart false, the last leaves it out
	if cs[0]["restart"] != false {
		t.Errorf("first call %v", cs[0])
	}
	if _, ok := cs[1]["restart"]; ok {
		t.Errorf("last call %v", cs[1])
	}
	if len(callsOf(f, "container_ctl")) != 0 {
		t.Error("restarted twice")
	}

	// a system container: the device answers with a note
	f.reply("apply_changes", map[string]interface{}{"restarted": false, "restart_note": "b is a system container: the change shows after it restarts"})
	rm, err := e.Remove(ctx, "b", []string{"etc/x"}, false)
	if err != nil || rm.Restarted || rm.RestartNote == "" {
		t.Fatalf("%v %+v", err, rm)
	}
	if len(callsOf(f, "container_ctl")) != 0 {
		t.Error("restarted a system container")
	}
}

func TestHubNoRestartSendsFalse(t *testing.T) {
	f, e := hubEnv(t)
	f.reply("apply_changes", map[string]interface{}{"restarted": false})
	f.reply("reset_changes", map[string]interface{}{"restarted": false})
	e.NoRestart = true
	if _, err := e.Remove(context.Background(), "a", []string{"y", "z"}, false); err != nil {
		t.Fatal(err)
	}
	if err := e.C.Reset(e.opCtx(context.Background(), true), "a"); err != nil {
		t.Fatal(err)
	}
	for _, c := range append(callsOf(f, "apply_changes"), callsOf(f, "reset_changes")...) {
		if c["restart"] != false {
			t.Errorf("call without restart:false: %v", c)
		}
	}
	if len(callsOf(f, "container_ctl")) != 0 {
		t.Error("restarted")
	}
}

func TestHubOldHubFallsBackToPvrRestart(t *testing.T) {
	f, e := hubEnv(t)
	f.reply("apply_changes", map[string]interface{}{})
	f.reply("container_ctl", map[string]interface{}{})
	r, err := e.Remove(context.Background(), "a", []string{"x"}, false)
	if err != nil || !r.Restarted || len(callsOf(f, "container_ctl")) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
}
