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

package libpvr

import (
	"fmt"
	"testing"
)

type testStruct struct {
	Field1 string `json:"field1"`
}

var (
	testMap1 = map[string]interface{}{
		"key1": "value1",
		"key2": "value2",
	}
	testStruct1 = testStruct{
		Field1: "Test1234",
	}
)

func ExampleSprintTmpl_basic() {
	s, _ := SprintTmpl("Basic", testMap1)
	fmt.Println(s)
	// Output: Basic
}

func ExampleSprintTmpl_simple1() {
	s, _ := SprintTmpl("Basic: {{ .key1 }}", testMap1)
	fmt.Println(s)
	// Output: Basic: value1
}

func ExampleSprintTmpl_sprintf1() {
	s, _ := SprintTmpl("Basic: {{ \"test\" | sprintf \"%.3s\" }}", testMap1)
	fmt.Println(s)
	// Output: Basic: tes
}

func ExampleSprintTmpl_sprintf2() {
	s, _ := SprintTmpl("Basic: {{ .key1 | sprintf \"%.3s\" }}", testMap1)
	fmt.Println(s)
	// Output: Basic: val
}

func ExampleSprintTmpl_struct() {
	s, _ := SprintTmpl("Basic: {{ .Field1 | sprintf \"%.3s\" }}", testStruct1)
	fmt.Println(s)
	// Output: Basic: Tes
}

func ExampleFixupRepoRef_ref1() {
	uristring, _ := FixupRepoRef("192.168.1.3")
	fmt.Println(uristring)
	// Output: http://192.168.1.3:12368/cgi-bin/pvr
}

func ExampleFixupRepoRef_ref2() {
	uristring, _ := FixupRepoRef("192.168.1.3#hash,some")
	fmt.Println(uristring)
	// Output: http://192.168.1.3:12368/cgi-bin/pvr#hash,some
}

func ExampleFixupRepoRef_ref3() {
	uristring, _ := FixupRepoRef("asacasa/test1234")
	fmt.Println(uristring)
	// Output: https://pvr.pantahub.com/asacasa/test1234
}

func ExampleFixupRepoRef_ref4() {
	uristring, _ := FixupRepoRef("asacasa/test1234#something,fun")
	fmt.Println(uristring)
	// Output: https://pvr.pantahub.com/asacasa/test1234#something,fun
}

func ExampleIsValidUrl_url1() {
	if IsValidUrl("asacasa/test") {
		fmt.Println("yes")
	} else {
		fmt.Println("no")
	}
	// Output: no
}

func ExampleIsValidUrl_url2() {
	if IsValidUrl("192.168.1.1") {
		fmt.Println("yes")
	} else {
		fmt.Println("no")
	}
	// Output: no
}

func ExampleIsValidUrl_url3() {
	if IsValidUrl("http://192.168.1.1") {
		fmt.Println("yes")
	} else {
		fmt.Println("no")
	}
	// Output: yes
}

func TestResolveDeviceEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		value   string
		want    string
	}{
		{
			name:    "bare id resolved against base url",
			baseURL: "https://api.stage.pantahub.com",
			value:   "000000000000000000000000",
			want:    "https://api.stage.pantahub.com/devices/000000000000000000000000",
		},
		{
			name:    "bare id resolved against base url with port and trailing slash",
			baseURL: "https://api.stage.pantahub.com:8443/",
			value:   "000000000000000000000000",
			want:    "https://api.stage.pantahub.com:8443/devices/000000000000000000000000",
		},
		{
			name:    "full url passed through unchanged",
			baseURL: "https://api.stage.pantahub.com",
			value:   "https://api.pantahub.com/devices/000000000000000000000000",
			want:    "https://api.pantahub.com/devices/000000000000000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDeviceEndpoint(tt.baseURL, tt.value)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveDeviceEndpoint(%q, %q) = %q, want %q", tt.baseURL, tt.value, got, tt.want)
			}
		})
	}
}
