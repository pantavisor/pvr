//
// Copyright 2017-2025  Pantacor Ltd.
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

package app

import (
	"encoding/json"
	"flag"
	"testing"

	"github.com/urfave/cli"
)

// newContext builds a cli.Context with the shared arg/config flags applied and
// the given argv parsed, mimicking how the commands receive them.
func newContext(t *testing.T, argv ...string) *cli.Context {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range TemplateArgConfigFlags() {
		f.Apply(fs)
	}
	if err := fs.Parse(argv); err != nil {
		t.Fatalf("parse %v: %v", argv, err)
	}
	return cli.NewContext(nil, fs, nil)
}

func TestParseTemplateArgs(t *testing.T) {
	c := newContext(t, "--arg", "PV_DEV_LOG=false", "--arg", "EMPTY", "--arg", "WITH=a=b")
	got, err := ParseTemplateArgs(c)
	if err != nil {
		t.Fatal(err)
	}
	if got["PV_DEV_LOG"] != "false" {
		t.Errorf("PV_DEV_LOG = %v, want false", got["PV_DEV_LOG"])
	}
	if got["EMPTY"] != "" {
		t.Errorf("bare key should map to empty string, got %v", got["EMPTY"])
	}
	if got["WITH"] != "a=b" {
		t.Errorf("only the first '=' should split: WITH = %v, want a=b", got["WITH"])
	}
}

func TestParseTemplateArgs_ConflictArgAndArgJSON(t *testing.T) {
	c := newContext(t, "--arg", "A=1", "--arg-json", "some.json")
	if _, err := ParseTemplateArgs(c); err == nil {
		t.Errorf("expected error when --arg and --arg-json are both set")
	}
}

func TestParseConfig_Typing(t *testing.T) {
	c := newContext(t,
		"--config", "num=5",
		"--config", "flag=true",
		"--config", "str=hello",
		"--config", "obj={\"x\":1}",
		"--config", "bare",
	)
	got := ParseConfig(c)

	// pvjson decodes with UseNumber, so numeric literals stay json.Number
	// (preserving precision) rather than being coerced to float64.
	if n, ok := got["num"].(json.Number); !ok || n.String() != "5" {
		t.Errorf("num = %#v, want json.Number(\"5\")", got["num"])
	}
	if b, ok := got["flag"].(bool); !ok || b != true {
		t.Errorf("flag = %#v, want bool(true)", got["flag"])
	}
	if got["str"] != "hello" {
		t.Errorf("str = %#v, want string fallback \"hello\"", got["str"])
	}
	if m, ok := got["obj"].(map[string]interface{}); !ok || m["x"] == nil {
		t.Errorf("obj = %#v, want parsed JSON object", got["obj"])
	}
	if got["bare"] != "" {
		t.Errorf("bare key should map to empty string, got %#v", got["bare"])
	}
}

func TestParseConfig_Empty(t *testing.T) {
	c := newContext(t)
	if got := ParseConfig(c); len(got) != 0 {
		t.Errorf("expected empty config, got %v", got)
	}
}
