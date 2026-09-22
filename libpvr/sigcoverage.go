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
	"fmt"
	"sort"

	"gitlab.com/pantacor/pvr/utils/pvjson"
)

// SignatureCoverage reports which keys of state the pvs@2 signature stored
// under sigKey protects and which its exclude patterns leave out, selected the
// way a device selects them. It does not verify the signature itself.
func SignatureCoverage(state PvrMap, sigKey string) (protected, excluded []string, err error) {
	signature, ok := state[sigKey].(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a signature of this state", sigKey)
	}

	stateBuf, err := pvjson.Marshal(state, pvjson.MarshalOptions{Compact: true})
	if err != nil {
		return nil, nil, err
	}

	summary, err := getSigSummary(stateBuf, state, signature)
	if err != nil {
		return nil, nil, err
	}

	protected = append([]string{}, summary.Protected...)
	excluded = append([]string{}, summary.Excluded...)
	sort.Strings(protected)
	sort.Strings(excluded)
	return protected, excluded, nil
}
