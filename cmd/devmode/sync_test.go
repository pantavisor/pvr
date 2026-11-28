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

package devmode

import (
	"archive/tar"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func imageTar(t *testing.T) []byte {
	return buildTar(t,
		tent{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "etc/version", mode: 0o644, body: "1"},
		tent{name: "etc/keep", mode: 0o644, body: "k"},
		tent{name: "etc/edit", mode: 0o644, body: "e"},
		tent{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "bin/sh", typ: tar.TypeSymlink, link: "busybox"},
		tent{name: "bin/busybox", mode: 0o755, body: "bb"},
	)
}

func TestSyncSendsOnlyWhatDiffersFromTheImage(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	put(t, e, "a", "etc/edit", "e2")
	put(t, e, "a", "etc/new", "n")
	os.Remove(filepath.Join(e.Dir, "a", "etc/version"))
	f.cmds = nil
	res, err := e.Sync(context.Background(), []string{"a"}, true)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "puts", calls(f, "pvcontrol devmode files put"), []string{"put a etc/edit - --uid 0 --gid 0 --mode 0644", "put a etc/new - --uid 0 --gid 0 --mode 0644"})
	eq(t, "removes", calls(f, "pvcontrol devmode files rm"), []string{"rm a etc/version --recursive"})
	if res.Containers[0].Skipped != 3 {
		t.Errorf("skipped = %d (want 3)", res.Containers[0].Skipped)
	}
	// the files of the image stay in the index
	if _, ok := mustIndex(t, e, "a").get("bin/busybox"); !ok {
		t.Error("image file lost from the index")
	}
}

func TestSyncResendsFilesTheDeviceHoldsInItsChanges(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	// an earlier push put etc/keep into the device changes, with the same content
	f.manifests = map[string]string{"a": `{"container":"a","files":[{"path":"etc/keep","type":"file","mode":"0644"}]}`}
	f.cmds = nil
	if _, err := e.Sync(context.Background(), []string{"a"}, true); err != nil {
		t.Fatal(err)
	}
	if got := calls(f, "pvcontrol devmode files put"); len(got) != 1 || !strings.Contains(got[0], "etc/keep") {
		t.Errorf("puts %q", got)
	}
}

func hubSyncEnv(t *testing.T, f *fakeDev) (*Env, *fakeHub) {
	e, _ := newEnv(t, f)
	pull(t, e, false)
	fh, h, _ := hubFixture(t)
	fh.reply("get_devmode_context", map[string]interface{}{"connected": true, "devmode": true})
	fh.reply("container_status", map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "a", "restart_policy": "container"}}})
	fh.reply("ls", map[string]interface{}{"entries": []interface{}{}})
	fh.reply("reset_changes", map[string]interface{}{})
	fh.reply("apply_changes", map[string]interface{}{"restarted": true})
	e.C = h
	e.BusyBase, e.BusyTotal = time.Millisecond, 40*time.Millisecond
	return e, fh
}

func TestHubSyncBatchesCalls(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	for i := 0; i < 600; i++ {
		put(t, e, "a", fmt.Sprintf("data/f%03d", i), "x")
	}
	if _, err := e.Sync(context.Background(), []string{"a"}, false); err != nil {
		t.Fatal(err)
	}
	var applies []toolCall
	for _, c := range fh.calls {
		if c.Tool == "apply_changes" {
			applies = append(applies, c)
		}
	}
	if len(applies) != 2 {
		t.Fatalf("%d apply_changes calls, want 2", len(applies))
	}
	if applies[0].Args["restart"] != false {
		t.Errorf("first call restart = %v", applies[0].Args["restart"])
	}
	if _, set := applies[1].Args["restart"]; set {
		t.Errorf("last call sets restart: %v", applies[1].Args["restart"])
	}
	if r := fh.last("reset_changes"); r["restart"] != false {
		t.Errorf("reset restart = %v", r["restart"])
	}
	n := 0
	for _, a := range applies {
		n += len(a.Args["ops"].([]interface{}))
	}
	if n != 602 { // 600 puts and a mkdir in each call
		t.Errorf("%d ops", n)
	}
}

func TestHubSyncRetriesBusy(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	put(t, e, "a", "etc/new", "n")
	n := 0
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		n++
		if n <= 2 {
			return nil, &toolFailure{Code: "BUSY", Message: "too many devmode commands for this device, try again in a few seconds"}
		}
		return map[string]interface{}{"restarted": true}, nil
	})
	if _, err := e.Sync(context.Background(), []string{"a"}, false); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("apply_changes called %d times", n)
	}
}

func TestHubSyncBusyForeverSaysWhatIsMissing(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	put(t, e, "a", "etc/new", "n")
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		return nil, &toolFailure{Code: "BUSY", Message: "too many devmode commands"}
	})
	_, err := e.Sync(context.Background(), []string{"a"}, false)
	if err == nil || !strings.Contains(err.Error(), "was reset") || !strings.Contains(err.Error(), "stayed busy") || !strings.Contains(err.Error(), "etc/new") {
		t.Fatalf("err = %v", err)
	}
	// the next sync sends the file the reset took away
	if p := mustIndex(t, e, "a").Pending; len(p) != 1 || p[0] != "etc/new" {
		t.Errorf("pending = %v", p)
	}
}

func TestPushRetriesBusy(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	put(t, e, "a", "etc/new", "n")
	n := 0
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		n++
		if n == 1 {
			return nil, &toolFailure{Code: "BUSY", Message: "busy"}
		}
		return map[string]interface{}{"restarted": true}, nil
	})
	if _, err := e.Push(context.Background(), []string{"a"}); err != nil || n < 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestBusyIncludesUpdateOngoing(t *testing.T) {
	if !isBusy(&Error{Msg: "Cannot do this operation while update is ongoing"}) {
		t.Fatal("an ongoing update must be retried like BUSY")
	}
	if isBusy(&Error{Msg: "Container not found"}) {
		t.Fatal("other errors must not be retried")
	}
}

func applyCalls(fh *fakeHub) []toolCall {
	var out []toolCall
	for _, c := range fh.calls {
		if c.Tool == "apply_changes" {
			out = append(out, c)
		}
	}
	return out
}

func TestHubPushBatchesCalls(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	for i := 0; i < 600; i++ {
		put(t, e, "a", fmt.Sprintf("data/f%03d", i), "x")
	}
	res, err := e.Push(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	applies := applyCalls(fh)
	if len(applies) != 2 {
		t.Fatalf("%d apply_changes calls, want 2", len(applies))
	}
	if applies[0].Args["restart"] != false {
		t.Errorf("first call restart = %v", applies[0].Args["restart"])
	}
	if _, set := applies[1].Args["restart"]; set {
		t.Errorf("last call sets restart: %v", applies[1].Args["restart"])
	}
	if len(res[0].Pushed) != 600 {
		t.Errorf("%d pushed", len(res[0].Pushed))
	}
	// everything is in the index now: a second push sends nothing
	before := len(applyCalls(fh))
	if _, err := e.Push(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if len(applyCalls(fh)) != before {
		t.Errorf("second push sent changes")
	}
}

func TestHubPushRetriesBusyPerCall(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	for i := 0; i < 600; i++ {
		put(t, e, "a", fmt.Sprintf("data/f%03d", i), "x")
	}
	n := 0
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		n++
		if n == 1 || n == 3 {
			return nil, &toolFailure{Code: "BUSY", Message: "too many devmode commands"}
		}
		return map[string]interface{}{"restarted": true}, nil
	})
	if _, err := e.Push(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("apply_changes called %d times, want 4", n)
	}
}

func TestHubPushPartialFailureSaysWhatIsPending(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": imageTar(t)}}
	e, fh := hubSyncEnv(t, f)
	for i := 0; i < 600; i++ {
		put(t, e, "a", fmt.Sprintf("data/f%03d", i), "x")
	}
	n := 0
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		n++
		if n == 2 {
			return nil, &toolFailure{Code: "FAILED", Message: "disk full"}
		}
		return map[string]interface{}{"restarted": true}, nil
	})
	res, err := e.Push(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "of 600 changes reached a") || !strings.Contains(err.Error(), "pending") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	sent := len(res[0].Pushed)
	if sent == 0 || sent >= 600 {
		t.Fatalf("%d pushed", sent)
	}
	// the next push sends only what did not get there
	fh.on("apply_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		return map[string]interface{}{"restarted": true}, nil
	})
	res, err = e.Push(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res[0].Pushed) != 600-sent {
		t.Errorf("second push sent %d, want %d", len(res[0].Pushed), 600-sent)
	}
}
