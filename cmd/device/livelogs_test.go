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

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

func TestValidateTail(t *testing.T) {
	for _, n := range []int{0, 1, 200, 500} {
		if err := validateTail(n); err != nil {
			t.Errorf("tail %d: %v", n, err)
		}
	}
	for _, n := range []int{-1, 501, 10000} {
		if err := validateTail(n); err == nil {
			t.Errorf("tail %d accepted", n)
		}
	}
}

func TestNormalizeFilter(t *testing.T) {
	ok := map[string]string{
		"":                       "",
		"   ":                    "",
		"  error ":               "error",
		"ctrl: timeout":          "ctrl: timeout",
		"héllo wörld":            "héllo wörld",
		strings.Repeat("a", 256): strings.Repeat("a", 256),
		"\t error":               "error",                  // trimmed, so no control character remains
		strings.Repeat("é", 128): strings.Repeat("é", 128), // 256 bytes
		"tab nbsp is printable":  "tab nbsp is printable",
	}
	for in, want := range ok {
		got, err := normalizeFilter(in)
		if err != nil || got != want {
			t.Errorf("normalizeFilter(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		strings.Repeat("a", 257),
		strings.Repeat("é", 129), // 258 bytes
		"a\tb",
		"bell\a",
		"esc\x1b[31m",
		"del\x7f",
		"c1\u0085x",
		"bad\xffutf8",
	}
	for _, in := range bad {
		if _, err := normalizeFilter(in); err == nil {
			t.Errorf("normalizeFilter(%q) accepted", in)
		}
	}
}

func TestParseLiveLogsOptions(t *testing.T) {
	o, err := parseLiveLogsOptions("", []string{"pantavisor/", "os/lxc/console.log", "pantavisor"}, 200, " err ", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if o.rev != "current" || !o.follow || o.filter != "err" {
		t.Errorf("unexpected options %+v", o)
	}
	if strings.Join(o.sources, ",") != "pantavisor,os/lxc/console.log" {
		t.Errorf("sources %q", o.sources)
	}

	o, err = parseLiveLogsOptions("locals/hub-3", []string{"a.log"}, 10, "", false, false, false)
	if err != nil || o.follow {
		t.Errorf("another revision must be read once: %+v, %v", o, err)
	}
	o, err = parseLiveLogsOptions("current", nil, 0, "", true, true, true)
	if err != nil || o.follow || len(o.sources) != 1 || o.sources[0] != "" || !o.json {
		t.Errorf("--all --no-follow: %+v, %v", o, err)
	}

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "f" + string(rune('a'+i))
	}
	for name, c := range map[string]struct {
		sources []string
		tail    int
		filter  string
		all     bool
	}{
		"tail below":   {tail: -1},
		"tail above":   {tail: 501},
		"long filter":  {filter: strings.Repeat("x", 257)},
		"ctl filter":   {filter: "a\x00b"},
		"dotdot":       {sources: []string{"../etc/passwd"}},
		"inner dotdot": {sources: []string{"a/../../b"}},
		"absolute":     {sources: []string{"/etc/passwd"}},
		"empty seg":    {sources: []string{"a//b"}},
		"too many":     {sources: eleven},
		"all+sources":  {sources: []string{"a"}, all: true},
	} {
		if _, err := parseLiveLogsOptions("current", c.sources, c.tail, c.filter, false, c.all, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCommandArgsFlagsAfterArguments(t *testing.T) {
	var gotArgs []string
	var noFollow bool
	var tail int
	var filter string
	cmd := CommandLiveLogs()
	cmd.Action = func(c *cli.Context) error {
		args, help, err := commandArgs(c)
		if err != nil || help {
			t.Fatalf("commandArgs: %v %v", help, err)
		}
		gotArgs, noFollow, tail, filter = args, c.Bool("no-follow"), c.Int("tail"), c.String("filter")
		return nil
	}
	app := cli.NewApp()
	app.Commands = []cli.Command{cmd}
	err := app.Run([]string{"pvr", "live-logs", "--tail", "5", "dev", "--no-follow", "pantavisor/pantavisor.log", "-f", "a b", "--", "--weird"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgs, "|") != "dev|pantavisor/pantavisor.log|--weird" || !noFollow || tail != 5 || filter != "a b" {
		t.Errorf("args %q no-follow %v tail %d filter %q", gotArgs, noFollow, tail, filter)
	}

	// aliases apply to the flag's primary name too
	err = app.Run([]string{"pvr", "live-logs", "dev", "-n=7", "--all"})
	if err != nil || tail != 7 || strings.Join(gotArgs, "|") != "dev" {
		t.Errorf("alias: args %q tail %d err %v", gotArgs, tail, err)
	}
}

func TestCommandArgsErrors(t *testing.T) {
	cmd := CommandLiveLogs()
	var gotErr error
	var gotHelp bool
	cmd.Action = func(c *cli.Context) error {
		_, gotHelp, gotErr = commandArgs(c)
		return nil
	}
	app := cli.NewApp()
	app.Commands = []cli.Command{cmd}
	app.Run([]string{"pvr", "live-logs", "dev", "--nope"})
	if gotErr == nil {
		t.Error("unknown flag accepted")
	}
	app.Run([]string{"pvr", "live-logs", "dev", "--filter"})
	if gotErr == nil {
		t.Error("flag without its value accepted")
	}
	app.Run([]string{"pvr", "live-logs", "dev", "--help"})
	if !gotHelp {
		t.Error("--help after the arguments not seen")
	}
}

const listingJSON = `{
  "rev": "current",
  "revisions": ["current", "locals/hub-3", "locals/hub-2"],
  "sources": [
    {"path": "telegraf/lxc/console.log", "size": 1204, "gz": false},
    {"path": "pantavisor/pantavisor.log.10.gz", "size": 2048, "gz": true},
    {"path": "pantavisor/pantavisor.log", "size": 48213, "gz": false},
    {"path": "pantavisor/pantavisor.log.2.gz", "size": 9120, "gz": true},
    {"path": "../escape.log", "size": 1},
    {"path": "/abs.log", "size": 1},
    {"path": "pantavisor/pantavisor.log", "size": 1},
    {"path": "boot.log", "size": 12}
  ]
}`

func TestSourceTreePrinting(t *testing.T) {
	l, err := parseLogListing([]byte(listingJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sources) != 5 {
		t.Fatalf("want the 5 valid, distinct sources, got %+v", l.Sources)
	}
	var b bytes.Buffer
	printSourceTree(&b, l)
	want := `Revision: current
Other revisions: locals/hub-3, locals/hub-2
pantavisor/
  pantavisor.log          47.1 KiB
  pantavisor.log.2.gz      8.9 KiB  gz
  pantavisor.log.10.gz     2.0 KiB  gz
telegraf/
  lxc/
    console.log            1.2 KiB
boot.log                      12 B
`
	if b.String() != want {
		t.Errorf("tree:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestSourceTreeEmptyAndTruncated(t *testing.T) {
	l, err := parseLogListing([]byte(`{"rev":"12","sources":[],"truncated":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	printSourceTree(&b, l)
	if !strings.Contains(b.String(), "Revision: 12") || !strings.Contains(b.String(), "No log files.") {
		t.Errorf("empty listing: %q", b.String())
	}
	if _, err := parseLogListing([]byte(`"not a listing"`)); err == nil {
		t.Error("a string is not a listing")
	}
}

func TestNaturalLess(t *testing.T) {
	cases := [][2]string{
		{"a.log.2.gz", "a.log.10.gz"},
		{"a", "b"},
		{"a", "a1"},
		{"x9", "x10"},
		{"x09", "x10"},
	}
	for _, c := range cases {
		if !naturalLess(c[0], c[1]) || naturalLess(c[1], c[0]) {
			t.Errorf("want %q < %q", c[0], c[1])
		}
	}
}

type recordedEvents struct {
	lines []string
	gaps  []int64
	drops []int64
}

func (r *recordedEvents) line(seq int64, l logLine) {
	r.lines = append(r.lines, l.Src+"|"+l.Line)
}
func (r *recordedEvents) gap(n int64)     { r.gaps = append(r.gaps, n) }
func (r *recordedEvents) dropped(n int64) { r.drops = append(r.drops, n) }

func i64(v int64) *int64 { return &v }

func TestLineStreamApply(t *testing.T) {
	s := newLineStream()
	ev := &recordedEvents{}
	// out of order, a duplicate, a gap (seq 2 missing) and a drop count
	n := s.apply(&logLinesView{Next: i64(3), Batches: []logBatch{
		{Seq: 1, Lines: []logLine{{"a", "two"}}},
		{Seq: 0, Lines: []logLine{{"a", "one"}}},
		{Seq: 3, Lines: []logLine{{"b", "four"}}, Dropped: 5},
	}}, ev)
	if n != 3 || s.after != 3 || s.lastSeq != 3 || s.ended {
		t.Errorf("n %d state %+v", n, s)
	}
	n = s.apply(&logLinesView{Next: i64(4), Batches: []logBatch{
		{Seq: 3, Lines: []logLine{{"b", "four again"}}},
		{Seq: 4, End: true, Reason: "done"},
	}}, ev)
	if n != 0 || !s.ended || s.reason != "done" {
		t.Errorf("end not seen: %+v", s)
	}
	if strings.Join(ev.lines, ",") != "a|one,a|two,b|four" {
		t.Errorf("lines %q", ev.lines)
	}
	if len(ev.gaps) != 1 || ev.gaps[0] != 1 || s.lost != 1 {
		t.Errorf("gaps %v", ev.gaps)
	}
	if len(ev.drops) != 1 || ev.drops[0] != 5 {
		t.Errorf("dropped %v", ev.drops)
	}

	// an empty answer keeps `after`; the view's own `ended` ends it
	s = newLineStream()
	s.apply(&logLinesView{}, ev)
	if s.after != -1 {
		t.Errorf("after moved to %d", s.after)
	}
	s.apply(&logLinesView{Next: i64(-1), Ended: true, Reason: "expired"}, ev)
	if !s.ended || s.reason != "expired" {
		t.Errorf("ended view: %+v", s)
	}
}

func TestLinePrinter(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &linePrinter{out: &out, errOut: &errOut}
	p.line(0, logLine{"a.log", "hello"})
	p.line(0, logLine{"", "only the first 64 files are streamed"})
	p.gap(2)
	p.dropped(1)
	if out.String() != "[a.log] hello\n" {
		t.Errorf("out %q", out.String())
	}
	if !strings.Contains(errOut.String(), "device: only the first") ||
		!strings.Contains(errOut.String(), "2 batches did not arrive") ||
		!strings.Contains(errOut.String(), "1 line was dropped") {
		t.Errorf("stderr %q", errOut.String())
	}

	out.Reset()
	p = &linePrinter{out: &out, errOut: &errOut, color: true}
	p.line(0, logLine{"a.log", "evil \x1b]0;title\x07 text"})
	if strings.Contains(out.String(), "\x1b]") || !strings.HasPrefix(out.String(), "\x1b[") {
		t.Errorf("tty output not neutralised or not coloured: %q", out.String())
	}

	out.Reset()
	p = &linePrinter{out: &out, errOut: &errOut, json: true}
	p.line(7, logLine{"a.log", "x\"y"})
	var v map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["src"] != "a.log" || v["line"] != "x\"y" || v["seq"] != float64(7) {
		t.Errorf("json line %q (%v)", out.String(), err)
	}
}

func TestDescribeLogEnd(t *testing.T) {
	for reason, fail := range map[string]bool{
		"done": false, "stopped": false, "deadline": false, "": false,
		"expired": true, "busy": true, "invalid": true, "invalid: no such rev": true, "open failed": true,
	} {
		if _, f := describeLogEnd(reason); f != fail {
			t.Errorf("%q: fail %v, want %v", reason, f, fail)
		}
	}
}

// liveLogsHub fakes a log session: lines answers from the given list, one
// per request, then blocks until the request is cancelled.
func liveLogsHub(t *testing.T, answers []logLinesView) *fakeHub {
	h := newFakeHub(t)
	h.handle("POST /log-sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "sess1", "expires_at": time.Now().Add(time.Minute)})
	})
	h.handle("POST /log-sessions/{sid}/renew", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "sess1"})
	})
	h.handle("DELETE /log-sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var served int32
	h.handle("GET /log-sessions/{sid}/lines", func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&served, 1)) - 1
		if i < len(answers) {
			want := "-1"
			if i > 0 && answers[i-1].Next != nil {
				want = strings.TrimSpace(string(mustJSON(*answers[i-1].Next)))
			}
			if got := r.URL.Query().Get("after"); got != want {
				t.Errorf("request %d asked after=%s, want %s", i, got, want)
			}
			writeJSON(w, http.StatusOK, answers[i])
			return
		}
		<-r.Context().Done()
	})
	return h
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func fastRunner(api *hubClient, req logSessionRequest, out, errOut *bytes.Buffer) *liveLogsRunner {
	r := newLiveLogsRunner(api, req, &linePrinter{out: out, errOut: errOut})
	r.renewEvery = 10 * time.Millisecond
	r.pollGap = 5 * time.Millisecond
	r.idleGap = 30 * time.Millisecond
	r.maxBackoff = 50 * time.Millisecond
	return r
}

func TestLiveLogsRunGapAndEnd(t *testing.T) {
	h := liveLogsHub(t, []logLinesView{
		{Next: i64(0), Batches: []logBatch{{Seq: 0, Lines: []logLine{{"pantavisor/pantavisor.log", "boot"}}}}},
		{Next: i64(0)}, // an empty long poll
		{Next: i64(2), Batches: []logBatch{{Seq: 2, Lines: []logLine{{"os/lxc/console.log", "up"}}, Dropped: 3}}},
		{Next: i64(3), Batches: []logBatch{{Seq: 3, End: true, Reason: "done"}}, Ended: true, Reason: "done"},
	})
	var out, errOut bytes.Buffer
	req := logSessionRequest{Rev: "current", Sources: []string{"pantavisor/pantavisor.log", "os"}, Tail: 50, Follow: false, Filter: "a"}
	reason, err := fastRunner(h.client(), req, &out, &errOut).run(context.Background())
	if err != nil || reason != "done" {
		t.Fatalf("reason %q err %v", reason, err)
	}
	if out.String() != "[pantavisor/pantavisor.log] boot\n[os/lxc/console.log] up\n" {
		t.Errorf("out %q", out.String())
	}
	if !strings.Contains(errOut.String(), "1 batch did not arrive") || !strings.Contains(errOut.String(), "3 lines were dropped") {
		t.Errorf("stderr %q", errOut.String())
	}
	var sent map[string]interface{}
	json.Unmarshal(h.body("POST /log-sessions"), &sent)
	if sent["rev"] != "current" || sent["tail"] != float64(50) || sent["follow"] != false || sent["filter"] != "a" ||
		len(sent["sources"].([]interface{})) != 2 {
		t.Errorf("session request %s", h.body("POST /log-sessions"))
	}
	if h.count("DELETE /log-sessions/{sid}") != 0 {
		t.Error("an ended session needs no DELETE")
	}
}

func TestLiveLogsRenewsAndDeletesOnCancel(t *testing.T) {
	h := liveLogsHub(t, []logLinesView{
		{Next: i64(0), Batches: []logBatch{{Seq: 0, Lines: []logLine{{"a.log", "x"}}}}},
	})
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// the second poll blocks; let a few renewals happen, then Ctrl-C
		for h.count("POST /log-sessions/{sid}/renew") < 3 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	done := make(chan struct{})
	var err error
	go func() {
		_, err = fastRunner(h.client(), logSessionRequest{Rev: "current", Sources: []string{""}, Tail: 200, Follow: true}, &out, &errOut).run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
	if h.count("DELETE /log-sessions/{sid}") != 1 {
		t.Errorf("DELETE calls: %d", h.count("DELETE /log-sessions/{sid}"))
	}
	if out.String() != "[a.log] x\n" {
		t.Errorf("out %q", out.String())
	}
	if !strings.Contains(string(h.body("POST /log-sessions")), `"sources":[""]`) {
		t.Errorf("--all must send the source \"\": %s", h.body("POST /log-sessions"))
	}
}

func TestLiveLogsRetriesThenFailsFatal(t *testing.T) {
	h := newFakeHub(t)
	h.handle("POST /log-sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "sess1"})
	})
	h.handle("DELETE /log-sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h.handle("POST /log-sessions/{sid}/renew", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "sess1"})
	})
	var n int32
	h.handle("GET /log-sessions/{sid}/lines", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeJSON(w, http.StatusBadGateway, map[string]string{"msg": "upstream"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"msg": "no scope"})
	})
	var out, errOut bytes.Buffer
	_, err := fastRunner(h.client(), logSessionRequest{Rev: "current", Sources: []string{"a"}}, &out, &errOut).run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no scope") || statusOf(err) != http.StatusForbidden {
		t.Errorf("err %v", err)
	}
	if !strings.Contains(errOut.String(), "retrying") {
		t.Errorf("a 502 is retried: %q", errOut.String())
	}
	if h.count("DELETE /log-sessions/{sid}") != 1 {
		t.Error("a failed session is stopped")
	}
}

func TestLiveLogsStartErrors(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusConflict:        "not connected to the Hub over MQTT",
		http.StatusTooManyRequests: "too many sessions",
		http.StatusBadRequest:      "bad sources",
	} {
		h := newFakeHub(t)
		st := status
		h.handle("POST /log-sessions", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, st, map[string]string{"msg": "bad sources"})
		})
		var out, errOut bytes.Buffer
		_, err := fastRunner(h.client(), logSessionRequest{Rev: "current", Sources: []string{"a"}}, &out, &errOut).run(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
}

func TestRunCommandPolls(t *testing.T) {
	h := newFakeHub(t)
	h.handle("POST /commands", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "cmd1", "cmd": "LIST_LOG_SOURCES", "status": "pending"})
	})
	var polls int32
	h.handle("GET /commands/{sid}", func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&polls, 1) {
		case 1:
			writeJSON(w, http.StatusOK, map[string]interface{}{"id": "cmd1", "status": "pending"})
		case 2:
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"msg": "later"})
		default:
			// the device's body as a JSON string, as the API passes non-JSON on
			writeJSON(w, http.StatusOK, map[string]interface{}{"id": "cmd1", "status": "ok", "code": 200, "output": listingJSON})
		}
	})
	raw, err := h.client().runCommand(context.Background(), listLogSourcesCmd, map[string]interface{}{"rev": "current"}, time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseLogListing(raw); err != nil {
		t.Errorf("output %s: %v", raw, err)
	}
	if !strings.Contains(string(h.body("POST /commands")), `"cmd":"LIST_LOG_SOURCES"`) ||
		!strings.Contains(string(h.body("POST /commands")), `"args":{"rev":"current"}`) {
		t.Errorf("command body %s", h.body("POST /commands"))
	}

	h2 := newFakeHub(t)
	h2.handle("POST /commands", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "cmd1", "status": "error", "code": 502, "error": "pv-ctrl unreachable"})
	})
	if _, err := h2.client().runCommand(context.Background(), listLogSourcesCmd, nil, time.Millisecond, time.Second); err == nil || !strings.Contains(err.Error(), "pv-ctrl unreachable") {
		t.Errorf("error result: %v", err)
	}
}

// TestSessionCaller checks the calls go through pvr's session (resty),
// with its bearer token and a JSON body.
func TestSessionCaller(t *testing.T) {
	h := newFakeHub(t)
	var auth, ctype string
	h.handle("POST /log-sessions", func(w http.ResponseWriter, r *http.Request) {
		auth, ctype = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		writeJSON(w, http.StatusCreated, map[string]interface{}{"id": "sess1"})
	})
	app := cli.NewApp()
	app.Metadata = map[string]interface{}{
		"PVR_CONFIG_DIR": t.TempDir(),
		"PVR_AUTH":       "tok123",
		"PVR_BASEURL":    h.srv.URL,
	}
	session, err := libpvr.NewSession(app)
	if err != nil {
		t.Fatal(err)
	}
	api := &hubClient{baseURL: h.srv.URL, deviceID: testDeviceID, call: sessionCaller(session)}
	s, err := api.createLogSession(context.Background(), logSessionRequest{Rev: "current", Sources: []string{"a"}, Tail: 1})
	if err != nil || s.ID != "sess1" {
		t.Fatalf("%+v %v", s, err)
	}
	if auth != "Bearer tok123" || !strings.HasPrefix(ctype, "application/json") {
		t.Errorf("auth %q content-type %q", auth, ctype)
	}
	if !strings.Contains(string(h.body("POST /log-sessions")), `"sources":["a"]`) {
		t.Errorf("body %s", h.body("POST /log-sessions"))
	}
}
