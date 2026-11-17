// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package devmode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli"
)

func intp(n int) *int { return &n }

func TestExecResultCode(t *testing.T) {
	for _, c := range []struct {
		r    ExecResult
		want int
	}{
		{ExecResult{ExitCode: intp(0)}, 0},
		{ExecResult{ExitCode: intp(3)}, 3},
		{ExecResult{Signal: intp(9)}, 137},
		{ExecResult{TimedOut: true, Signal: intp(15)}, 124},
	} {
		if got := c.r.Code(); got != c.want {
			t.Errorf("%+v: got %d want %d", c.r, got, c.want)
		}
	}
}

func TestPvcontrolExec(t *testing.T) {
	rt := &recTransport{}
	c := &Client{T: rt}
	r, err := c.Exec(context.Background(), "web", []string{"sh", "-c", "echo 'a b'"},
		ExecOpts{Timeout: 5 * time.Second, Cwd: "/srv", Env: map[string]string{"B": "2", "A": "1 x"}, MaxOutput: 100, Stdin: []byte("in")})
	if err != nil || r.Code() != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	want := `pvcontrol containers exec --timeout 5 --stdin --max-output 100 --cwd /srv --env 'A=1 x' --env B=2 web -- sh -c 'echo '\''a b'\'''`
	if len(rt.cmds) != 1 || rt.cmds[0] != want {
		t.Errorf("got %q\nwant %q", rt.cmds, want)
	}
}

// scriptTransport answers commands with a function.
type scriptTransport struct {
	cmds []string
	run  func(cmd string) (int, string, string)
}

func (s *scriptTransport) Run(_ context.Context, c string, _ io.Reader, out, errw io.Writer) (int, error) {
	s.cmds = append(s.cmds, c)
	code, o, e := s.run(c)
	io.WriteString(out, o)
	io.WriteString(errw, e)
	return code, nil
}
func (s *scriptTransport) Close() error { return nil }

func TestPvcontrolLogs(t *testing.T) {
	st := `{"enabled":true,"active":true,"revision":"devmode/r/0"}`
	tr := &scriptTransport{run: func(c string) (int, string, string) {
		if strings.HasPrefix(c, "pvcontrol devmode status") {
			return 0, st, ""
		}
		if strings.Contains(c, "/web/") {
			return 0, "one\r\ntwo\n", ""
		}
		return 1, "", "tail: can't open '/x': No such file or directory\n"
	}}
	c := &Client{T: tr}
	lines, err := c.Logs(context.Background(), "web", 2)
	if err != nil || strings.Join(lines, ",") != "one,two" {
		t.Fatalf("%v %v", lines, err)
	}
	if want := "tail -n 2 /pantavisor/logs/devmode/r/0/web/lxc/console.log"; tr.cmds[1] != want {
		t.Errorf("got %q want %q", tr.cmds[1], want)
	}
	_, err = c.Logs(context.Background(), "gone", 2)
	if e, ok := err.(*Error); !ok || e.Kind != KindGeneric || !strings.Contains(e.Msg, "/pantavisor/logs/devmode/r/0/gone/lxc/console.log") || !strings.Contains(e.Msg, "No such file") {
		t.Errorf("missing file: %v", err)
	}
	if _, err := c.Logs(context.Background(), "../etc", 2); err == nil || err.(*Error).Kind != KindUsage {
		t.Errorf("bad name: %v", err)
	}
	st = `{"enabled":true,"active":false}`
	if _, err := c.Logs(context.Background(), "web", 2); err == nil || err.(*Error).Kind != KindNotActive {
		t.Errorf("not active: %v", err)
	}
}

func TestHTTPLogs(t *testing.T) {
	var query string
	f := &fakeCGI{reply: func(call string) (int, string) {
		return 200, `{"enabled":true,"active":true,"revision":"devmode/r/0"}`
	}}
	h := newHTTPBackend(t, f)
	mux := http.NewServeMux()
	mux.Handle(cgiPrefix+"/", f)
	mux.HandleFunc("/cgi-bin/logs", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		if strings.Contains(query, "source=gone/") {
			w.WriteHeader(404)
			io.WriteString(w, "no such log\n")
			return
		}
		io.WriteString(w, "one\ntwo\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h.Base = srv.URL
	lines, err := h.Logs(context.Background(), "web", 2)
	if err != nil || strings.Join(lines, ",") != "one,two" {
		t.Fatalf("%v %v", lines, err)
	}
	if query != "rev=devmode/r/0&source=web/lxc/console.log&tail=1&n=2" {
		t.Errorf("query %q", query)
	}
	if _, err := h.Logs(context.Background(), "gone", 2); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing: %v", err)
	}
}

func TestHTTPExec(t *testing.T) {
	f := &fakeCGI{reply: func(call string) (int, string) {
		return 200, `{"exit_code":3,"signal":null,"timed_out":false,"stdout_b64":"` + base64.StdEncoding.EncodeToString([]byte("out\n")) +
			`","stderr_b64":"` + base64.StdEncoding.EncodeToString([]byte("err\n")) + `","truncated":{"stdout":false,"stderr":true},"duration_ms":7}`
	}}
	h := newHTTPBackend(t, f)
	r, err := h.Exec(context.Background(), "web", []string{"cat", "/etc/os-release"}, ExecOpts{Stdin: []byte("x"), Env: map[string]string{"K": "v"}})
	if err != nil || r.Code() != 3 || r.Stdout != "out\n" || r.Stderr != "err\n" || !r.TruncStderr || r.DurationMS != 7 {
		t.Fatalf("%+v %v", r, err)
	}
	var sent map[string]interface{}
	json.Unmarshal([]byte(f.body["POST /containers/web/exec"]), &sent)
	if sent["timeout_s"] != float64(30) || sent["stdin_b64"] != "eA==" || sent["argv"].([]interface{})[0] != "cat" {
		t.Errorf("sent %v", sent)
	}
	// a stopped container is an error, not a result
	f2 := &fakeCGI{reply: func(string) (int, string) { return 409, `{"Error":"Not running a devmode revision"}` }}
	if _, err := newHTTPBackend(t, f2).Exec(context.Background(), "web", []string{"ls"}, ExecOpts{}); err == nil || err.(*Error).Kind != KindNotActive {
		t.Errorf("409: %v", err)
	}
}

func TestHubExecAndLogs(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	f.reply("exec", map[string]interface{}{"exit_code": 2, "timed_out": false, "stdout": base64.StdEncoding.EncodeToString([]byte{0xff, 'a'}), "stdout_encoding": "base64",
		"stderr": "bad\n", "stderr_encoding": "text", "truncated": map[string]interface{}{"stdout": true, "stderr": false}, "duration_ms": 5})
	r, err := h.Exec(ctx, "web", []string{"ls", "/x"}, ExecOpts{Timeout: 10 * time.Second, Stdin: []byte("in"), Cwd: "/", Env: map[string]string{"A": "b"}, MaxOutput: 50})
	if err != nil || r.Code() != 2 || r.Stdout != "\xffa" || r.Stderr != "bad\n" || !r.TruncStdout {
		t.Fatalf("%+v %v", r, err)
	}
	a := f.last("exec")
	if a["timeout_s"] != float64(10) || a["stdin_base64"] != "aW4=" || a["cwd"] != "/" || a["max_output"] != float64(50) || a["container"] != "web" {
		t.Errorf("exec args %v", a)
	}
	// exec needs the lease
	if n := f.names(); n[0] != "lease" || n[1] != "exec" {
		t.Errorf("calls %v", n)
	}

	f.reply("logs", map[string]interface{}{"lines": []interface{}{map[string]interface{}{"src": "console", "text": "one"}, map[string]interface{}{"src": "console", "text": "two"}}})
	lines, err := h.Logs(ctx, "web", 2)
	if err != nil || strings.Join(lines, ",") != "one,two" {
		t.Fatalf("%v %v", lines, err)
	}
	if a := f.last("logs"); a["tail"] != float64(2) || a["container"] != "web" {
		t.Errorf("logs args %v", a)
	}
}

// the command line passes everything after -- to the command
func TestExecCommandLine(t *testing.T) {
	f := &fakeCGI{reply: func(call string) (int, string) {
		switch call {
		case "GET /devmode":
			return 200, `{"enabled":true,"active":true,"revision":"devmode/r/1","base":"1"}`
		}
		return 200, `{"exit_code":4,"signal":null,"timed_out":false,"stdout_b64":"","stderr_b64":"","truncated":{"stdout":false,"stderr":false}}`
	}}
	srv := newHTTPBackend(t, f)
	app := cli.NewApp()
	app.Metadata = map[string]interface{}{"PVR_CONFIG_DIR": t.TempDir()}
	app.Commands = []cli.Command{CommandDevmode()}
	var code int
	oldExit := cli.OsExiter
	cli.OsExiter = func(c int) { code = c }
	defer func() { cli.OsExiter = oldExit }()
	err := app.Run([]string{"pvr", "devmode", "exec", srv.Base, "web", "--timeout", "7s", "--", "ls", "-l", "--json"})
	if code != 4 {
		t.Errorf("exit code %d, err %v", code, err)
	}
	var sent struct {
		Argv     []string `json:"argv"`
		TimeoutS int      `json:"timeout_s"`
	}
	json.Unmarshal([]byte(f.body["POST /containers/web/exec"]), &sent)
	if strings.Join(sent.Argv, " ") != "ls -l --json" || sent.TimeoutS != 7 {
		t.Errorf("sent %+v", sent)
	}
	// missing command
	err = app.Run([]string{"pvr", "devmode", "exec", srv.Base, "web"})
	if code != 2 {
		t.Errorf("usage exit code %d", code)
	}
}

// A shell on the host finds the console log under PV_LOG_DIR.
func TestPvcontrolLogsOnTheHost(t *testing.T) {
	tr := &scriptTransport{run: func(c string) (int, string, string) {
		switch {
		case strings.HasPrefix(c, "pvcontrol devmode status"):
			return 0, `{"enabled":true,"active":true,"revision":"devmode/r/0"}`, ""
		case strings.HasPrefix(c, "pvcontrol conf ls"):
			return 0, `[{"key":"PV_LOG_DIR","value":"/storage/logs/","modified":"default"}]`, ""
		case c == "tail -n 3 /storage/logs/devmode/r/0/web/lxc/console.log":
			return 0, "host line\n", ""
		}
		return 1, "", "tail: can't open: No such file or directory\n"
	}}
	lines, err := (&Client{T: tr}).Logs(context.Background(), "web", 3)
	if err != nil || strings.Join(lines, ",") != "host line" {
		t.Fatalf("%v %v (%q)", lines, err, tr.cmds)
	}
}
