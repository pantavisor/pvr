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

// Package pvjson is the single entry point for JSON encoding and decoding in
// pvr. Every other package must go through Marshal/Unmarshal here instead of
// calling encoding/json or canonicaljson-go directly.
//
// The reason is correctness: decoding always uses UseNumber so that large
// integers (e.g. uids beyond 2^53) survive a decode/encode round-trip instead
// of being silently rounded through float64. Encoding then handles json.Number
// transparently, including in canonical mode.
package pvjson

import (
	"bytes"
	"encoding/json"

	cjson "github.com/gibson042/canonicaljson-go"
)

// MarshalOptions selects the output format for Marshal. The zero value
// produces four-space indented JSON (the historical pvr default).
type MarshalOptions struct {
	// Canonical encodes using canonical JSON (deterministic, hash-stable
	// output). Takes precedence over every other option.
	Canonical bool

	// Compact encodes without any indentation, like encoding/json.Marshal.
	Compact bool

	// Prefix and Indent control pretty-printing when neither Canonical nor
	// Compact is set. When both are empty, output is indented with four
	// spaces.
	Prefix string
	Indent string
}

// Marshal encodes v as JSON. Without options it pretty-prints with four-space
// indentation. Use MarshalOptions to select canonical, compact or custom
// indentation. json.Number values are always preserved exactly.
func Marshal(v interface{}, opts ...MarshalOptions) ([]byte, error) {
	var options MarshalOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	if options.Canonical {
		return cjson.Marshal(v)
	}

	if options.Compact {
		return json.Marshal(v)
	}

	indent := options.Indent
	if indent == "" && options.Prefix == "" {
		indent = "    "
	}

	return json.MarshalIndent(v, options.Prefix, indent)
}

// Unmarshal decodes JSON into v, always using UseNumber so numeric literals are
// preserved as json.Number rather than being coerced to float64 (which would
// lose precision for large integers).
func Unmarshal(data []byte, v interface{}) error {
	decoder := json.NewDecoder(bytes.NewBuffer(data))
	decoder.UseNumber()

	if err := decoder.Decode(v); err != nil {
		return err
	}

	return nil
}
