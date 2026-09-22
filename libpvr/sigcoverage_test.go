// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//	See the License for the specific language governing permissions and
//	limitations under the License.
package libpvr

import (
	"os"
	"reflect"
	"testing"

	"gitlab.com/pantacor/pvr/utils/pvjson"
)

func TestSignatureCoverage(t *testing.T) {
	buf, err := os.ReadFile("../_test/expected/test_remove_part_already_signed_step1.json")
	if err != nil {
		t.Fatal(err)
	}
	state := PvrMap{}
	if err := pvjson.Unmarshal(buf, &state); err != nil {
		t.Fatal(err)
	}

	protected, excluded, err := SignatureCoverage(state, "_sigs/nginx.json")
	if err != nil {
		t.Fatal(err)
	}
	wantProtected := []string{
		"_config/nginx/etc/nginx/nginx.conf",
		"nginx/lxc.container.conf",
		"nginx/root.squashfs",
		"nginx/root.squashfs.docker-digest",
		"nginx/run.json",
		// The exclude pattern "src.json" only names a key at the root.
		"nginx/src.json",
	}
	if !reflect.DeepEqual(protected, wantProtected) {
		t.Errorf("protected = %v, want %v", protected, wantProtected)
	}
	if !reflect.DeepEqual(excluded, []string{"_sigs/nginx.json"}) {
		t.Errorf("excluded = %v", excluded)
	}

	if _, _, err := SignatureCoverage(state, "nginx/run.json"); err == nil {
		t.Error("a key that is no signature must be refused")
	}
	if _, _, err := SignatureCoverage(state, "_sigs/missing.json"); err == nil {
		t.Error("a missing signature must be refused")
	}
}
