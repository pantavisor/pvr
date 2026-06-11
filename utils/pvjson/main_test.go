//
// Copyright 2017-2023  Pantacor Ltd.
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

package pvjson

import (
	"strings"
	"testing"
)

// Large integers must survive a decode/encode round-trip exactly. Plain
// encoding/json decodes numbers into float64, which only has 53 bits of
// mantissa and would round 9000000000000000001 down to 9000000000000000000.
func TestBigIntegerRoundTrip(t *testing.T) {
	const bigInt = "9000000000000000001"
	src := []byte(`{"uid": ` + bigInt + `}`)

	modes := map[string][]MarshalOptions{
		"default":   nil,
		"canonical": {{Canonical: true}},
		"compact":   {{Compact: true}},
	}

	for name, opts := range modes {
		var v interface{}
		if err := Unmarshal(src, &v); err != nil {
			t.Fatalf("%s: Unmarshal: %v", name, err)
		}
		out, err := Marshal(v, opts...)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if !strings.Contains(string(out), bigInt) {
			t.Errorf("%s: big integer not preserved: got %s", name, out)
		}
	}
}
