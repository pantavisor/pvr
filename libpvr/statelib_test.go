// Copyright 2022  Pantacor Ltd.
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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

type args struct {
	srcBuff   []byte
	patchBuff []byte
	srcFrags  string
	destFrag  string
	merge     bool
	state     *PvrMap
}

type Test struct {
	args    args
	want    map[string]interface{}
	wantErr bool
}

func TestPatchState(t *testing.T) {

	t.Run("Update state without fragments", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\"}"),
				srcFrags:  "",
				destFrag:  "",
				merge:     true,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value2",
			},
		}

		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Update with patch fragment", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\", \"key2\":\"value2\"}"),
				srcFrags:  "",
				destFrag:  "key2",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value1",
				"key2":  "value2",
			},
		}

		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Merge with patch fragment", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\", \"key2\":\"value2\"}"),
				srcFrags:  "",
				destFrag:  "key2",
				merge:     true,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value1",
				"key2":  "value2",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Update with source fragment negative", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key2\":\"value2\"}"),
				srcFrags:  "-key1",
				destFrag:  "",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key2":  "value2",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Update with source fragment positive", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\",\"key2\":\"value2\"}"),
				srcFrags:  "key1",
				destFrag:  "",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value2",
				"key2":  "value2",
			},
		}

		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Update with dest fragment negative", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\",\"key2\":\"value2\"}"),
				srcFrags:  "key1",
				destFrag:  "-key2",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value2",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Copy with new name src frag", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{}"),
				srcFrags:  "key1",
				destFrag:  "key2",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key2":  "value1",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Copy multi with new name src frag", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\",\"key2\":\"value2\"}"),
				patchBuff: []byte("{}"),
				srcFrags:  "key1,key2",
				destFrag:  "key3,key1",
				merge:     false,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value2",
				"key3":  "value1",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Merge states", func(t *testing.T) {
		tt := Test{
			args: args{
				srcBuff:   []byte("{\"key1\":\"value1\"}"),
				patchBuff: []byte("{\"key1\":\"value2\",\"key2\":\"value2\"}"),
				srcFrags:  "",
				destFrag:  "",
				merge:     true,
				state:     nil,
			},
			want: map[string]interface{}{
				"#spec": "pantavisor-service-system@1",
				"key1":  "value2",
				"key2":  "value2",
			},
		}
		_, got, err := PatchState(tt.args.srcBuff, tt.args.patchBuff, tt.args.srcFrags, tt.args.destFrag, tt.args.merge, tt.args.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatchState() = %v, want %v", got, tt.want)
		}
	})

	t.Run("Merge new bsp to device", func(t *testing.T) {
		srcBuff, err := os.ReadFile("../testdata/statelib/merge_new_bsp_to_device/src.json")
		if err != nil {
			t.Error(err)
			return
		}
		patchBuff, err := os.ReadFile("../testdata/statelib/merge_new_bsp_to_device/patch.json")
		if err != nil {
			t.Error(err)
			return
		}
		wantBuff, err := os.ReadFile("../testdata/statelib/merge_new_bsp_to_device/want.json")
		if err != nil {
			t.Error(err)
			return
		}

		var wantMap PvrMap
		err = pvjson.Unmarshal(wantBuff, &wantMap)
		if err != nil {
			t.Error(err)
			return
		}

		want, err := pvjson.Marshal(wantMap, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			t.Error(err)
			return
		}

		srcFrags := ""
		destFrag := ""
		merge := true
		wantErr := false

		_, got, err := PatchState(srcBuff, patchBuff, srcFrags, destFrag, merge, nil)
		if (err != nil) != wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, wantErr)
			return
		}
		result, err := pvjson.Marshal(got, pvjson.MarshalOptions{Canonical: true})
		if (err != nil) != wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, wantErr)
			return
		}

		assert.Equal(t, string(result), string(want))
	})

	t.Run("remove config signed package", func(t *testing.T) {
		srcBuff, err := os.ReadFile("../testdata/statelib/remove_config_signed_package/src.json")
		if err != nil {
			t.Error(err)
			return
		}
		patchBuff, err := os.ReadFile("../testdata/statelib/remove_config_signed_package/patch.json")
		if err != nil {
			t.Error(err)
			return
		}
		wantBuff, err := os.ReadFile("../testdata/statelib/remove_config_signed_package/want.json")
		if err != nil {
			t.Error(err)
			return
		}

		var wantMap PvrMap
		err = pvjson.Unmarshal(wantBuff, &wantMap)
		if err != nil {
			t.Error(err)
			return
		}

		want, err := pvjson.Marshal(wantMap, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			t.Error(err)
			return
		}

		srcFrags := ""
		destFrag := ""
		merge := false
		wantErr := false

		_, got, err := PatchState(srcBuff, patchBuff, srcFrags, destFrag, merge, nil)
		if (err != nil) != wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, wantErr)
			return
		}
		result, err := pvjson.Marshal(got, pvjson.MarshalOptions{Canonical: true})
		if (err != nil) != wantErr {
			t.Errorf("PatchState() error = %v, wantErr %v", err, wantErr)
			return
		}

		assert.Equal(t, string(want), string(result))
	})
}

// Regression: state diffing (jsondiff/MergePatch) must not round large
// integers through float64. jsondiff.Operation implements json.Marshaler and
// canonical encoding used to re-parse its output without UseNumber, which
// rounded 9000000000000000001 down to 9000000000000000000.
func TestBigNumberPreservedInStateDiff(t *testing.T) {
	const bigInt = "9000000000000000001"
	src := []byte(`{"#spec":"pantavisor-service-system@1"}`)
	target := []byte(`{"#spec":"pantavisor-service-system@1","test.json":{"uid":` + bigInt + `}}`)

	t.Run("GetStatePatch", func(t *testing.T) {
		patch, err := GetStatePatch(src, target)
		if err != nil {
			t.Fatalf("GetStatePatch: %v", err)
		}
		if !strings.Contains(string(*patch), bigInt) {
			t.Errorf("big integer not preserved in state patch: %s", *patch)
		}
	})

	t.Run("MergePatch", func(t *testing.T) {
		merged, err := MergePatch(src, target)
		if err != nil {
			t.Fatalf("MergePatch: %v", err)
		}
		if !strings.Contains(string(merged), bigInt) {
			t.Errorf("big integer not preserved in merged state: %s", merged)
		}
	})
}
