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
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	jsonpatch "github.com/asac/json-patch"
	"github.com/highercomve/jsondiff"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

func copyState(state map[string]interface{}) map[string]interface{} {
	data, err := pvjson.Marshal(state, pvjson.MarshalOptions{Compact: true})
	if err != nil {
		return state
	}
	var result map[string]interface{}
	err = pvjson.Unmarshal(data, &result)
	if err != nil {
		return state
	}
	return result
}

func AddFragsToState(srcState, patchState map[string]interface{}, frags string) map[string]interface{} {
	pJSONMap := copyState(srcState)

	unpartPrefixes := []string{}
	if frags != "" {
		parsePrefixes := strings.Split(frags, ",")
		for _, v := range parsePrefixes {
			if strings.HasPrefix(v, "-") {
				unpartPrefixes = append(unpartPrefixes, v[1:])
			}
		}
	}

	for _, partPrefix := range unpartPrefixes {
		for k := range pJSONMap {
			if strings.HasPrefix(k, partPrefix) {
				delete(pJSONMap, k)
			}
		}
	}

	for k1, v := range patchState {
		for k2 := range pJSONMap {
			if k1 == k2 {
				delete(pJSONMap, k1)
			}
		}
		pJSONMap[k1] = v
	}

	return pJSONMap
}

func ExpandFragmentsWithSignedParts(src PvrMap, fragments string) (string, error) {
	result := strings.Split(fragments, ",")
	srcBuf, err := pvjson.Marshal(src, pvjson.MarshalOptions{Compact: true})
	if err != nil {
		return "", err
	}

	for _, frag := range strings.Split(fragments, ",") {
		if !strings.HasPrefix(frag, "_sigs/") {
			continue
		}

		summary, err := getSigSummary(srcBuf, src, src[frag].(map[string]interface{}))
		if err != nil {
			return "", err
		}
		result = append(result, summary.Excluded...)
		result = append(result, summary.Protected...)
	}

	return strings.Join(result, ","), nil
}

func FilterByFrags(state map[string]interface{}, frags string) (map[string]interface{}, error) {
	pJSONMap := copyState(state)

	partPrefixes := []string{}
	unpartPrefixes := []string{}
	if frags != "" {
		parsePrefixes := strings.Split(frags, ",")
		for _, v := range parsePrefixes {
			if !strings.HasPrefix(v, "-") {
				partPrefixes = append(partPrefixes, v)
			} else {
				unpartPrefixes = append(unpartPrefixes, v[1:])
			}
		}
	}

	for k := range pJSONMap {
		found := true
		for _, v := range partPrefixes {
			if k == v {
				found = true
				break
			}
			if !strings.HasSuffix(v, "/") {
				v += "/"
			}
			if strings.HasPrefix(k, v) {
				found = true
				break
			} else {
				found = false
			}
		}
		if !found {
			delete(pJSONMap, k)
		}
	}

	for _, partPrefix := range partPrefixes {
		// remove all files for name "app/"
		for k := range pJSONMap {
			if strings.HasPrefix(k, partPrefix) {
				delete(pJSONMap, k)
			}
		}

		// add back all from new map
		for k, v := range state {
			if strings.HasPrefix(k, partPrefix) {
				pJSONMap[k] = v
			}
		}
	}

	for _, unpartPrefix := range unpartPrefixes {
		// remove all files for name "app/"
		for k := range pJSONMap {
			if strings.HasPrefix(k, unpartPrefix) {
				delete(pJSONMap, k)
			}
		}
	}

	return pJSONMap, nil
}

func OverwriteState(state PvrMap, newState PvrMap) {
	for key := range state {
		delete(state, key)
	}
	for key, value := range newState {
		state[key] = value
	}
}

// PatchState update a json with a patch
func PatchState(srcBuff, patchBuff []byte, srcFrags, patchFrag string, merge bool, state *PvrMap) ([]byte, map[string]interface{}, error) {
	var srcState PvrMap
	var patchState PvrMap

	err := pvjson.Unmarshal(srcBuff, &srcState)
	if err != nil {
		return nil, nil, err
	}

	err = pvjson.Unmarshal(patchBuff, &patchState)
	if err != nil {
		return nil, nil, err
	}

	if len(patchState) == 0 && len(strings.Split(patchFrag, ",")) == len(strings.Split(srcFrags, ",")) {
		srcState, err = renameKeys(srcState, srcFrags, patchFrag)
		if err != nil {
			return nil, nil, err
		}
	}

	patchState, err = FilterByFrags(patchState, patchFrag)
	if err != nil {
		return nil, nil, err
	}

	// TODO: here before running the merge or adding alg
	// i should add the logic to search for _sigs in the patch, then look for same signatures
	// in the src state, decode those signatures and delete everything in the include, exclude from
	// src state, and after that execute the normal merge of states.
	err = CleanPatchSignedPkg(&srcState, &patchState, nil)
	if err != nil {
		return nil, nil, err
	}

	var jsonMerged []byte
	if merge {
		pJSONMap, err := FilterByFrags(srcState, srcFrags)
		if err != nil {
			return nil, nil, err
		}

		srcJsonMap, err := pvjson.Marshal(pJSONMap, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			return nil, nil, err
		}

		jsonDataSelect, err := pvjson.Marshal(patchState, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			return nil, nil, err
		}

		jsonMerged, err = jsonpatch.MergePatch(srcJsonMap, jsonDataSelect)
		if err != nil {
			return nil, nil, err
		}
	} else {
		jsonMap := AddFragsToState(srcState, patchState, srcFrags)
		jsonMerged, err = pvjson.Marshal(jsonMap, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			return nil, nil, err
		}
	}

	if _, ok := patchState["#spec"]; !ok {
		patchState["#spec"] = "pantavisor-service-system@1"
	}

	err = pvjson.Unmarshal(jsonMerged, &patchState)
	if err != nil {
		return nil, nil, err
	}

	if state != nil {
		OverwriteState(*state, patchState)
	}

	for key, value := range patchState {
		if value == nil {
			delete(patchState, key)
		}
	}

	jsonMerged, err = pvjson.Marshal(&patchState, pvjson.MarshalOptions{Canonical: true})
	if err != nil {
		return nil, nil, err
	}

	return jsonMerged, patchState, err
}

func GetStatePatch(initialState, endState []byte) (*[]byte, error) {
	var diffbytes []byte
	diff, err := jsondiff.CompareJSON(initialState, endState)
	if err != nil {
		return nil, err
	}

	if diff != nil {
		// The diff is an intermediate RFC6902 patch that gets applied
		// downstream, so it must not be canonicalized: jsondiff.Operation
		// implements json.Marshaler and canonical encoding re-parses that
		// output without UseNumber, rounding large integers through float64.
		// A plain (compact) encode preserves json.Number exactly.
		diffbytes, err = pvjson.Marshal(diff, pvjson.MarshalOptions{Compact: true})
		if err != nil {
			return nil, err
		}
	}

	return &diffbytes, err
}

func renameKeys(state map[string]interface{}, srcFrags, patchFrag string) (map[string]interface{}, error) {
	pJSONMap := map[string]interface{}{}
	srcPrefixes := strings.Split(srcFrags, ",")
	pathPrefixes := strings.Split(patchFrag, ",")

	for index, key := range srcPrefixes {
		newKey := pathPrefixes[index]
		pJSONMap[newKey] = state[key]
	}

	return pJSONMap, nil
}

func MergePatch(srcData, target []byte) ([]byte, error) {
	var srcState PvrMap
	var targetState PvrMap
	var patchState PvrMap

	patchData, err := jsonpatch.CreateMergePatch(srcData, target)
	if err != nil {
		return nil, err
	}

	err = pvjson.Unmarshal(srcData, &srcState)
	if err != nil {
		return nil, err
	}

	err = pvjson.Unmarshal(target, &targetState)
	if err != nil {
		return nil, err
	}

	err = pvjson.Unmarshal(patchData, &patchState)
	if err != nil {
		return nil, err
	}

	err = CleanPatchSignedPkg(&srcState, &patchState, &targetState)
	if err != nil {
		return nil, err
	}

	srcData, err = pvjson.Marshal(srcState, pvjson.MarshalOptions{Canonical: true})
	if err != nil {
		return nil, err
	}

	target, err = pvjson.Marshal(targetState, pvjson.MarshalOptions{Canonical: true})
	if err != nil {
		return nil, err
	}

	statePatch, err := GetStatePatch(srcData, target)
	if err != nil {
		return nil, err
	}

	if statePatch == nil || len(*statePatch) == 0 {
		return srcData, nil
	}

	patch, err := jsonpatch.DecodePatch(*statePatch)
	if err != nil {
		return nil, err
	}

	merged, err := patch.Apply(srcData)
	if err != nil {
		return nil, err
	}

	return merged, nil

}

// CleanRemovedSignatures cleans the removed signatures in the initial state by comparing it with the final state.
//
// Parameters:
// - initialState: A pointer to the initial state PvrMap.
// - finalState: A pointer to the final state PvrMap.
//
// Returns:
// - error: An error if any occurred during the cleaning process.
func CleanRemovedSignatures(initialState, finalState *PvrMap, initialStateBuf []byte) (err error) {
	deleted := []string{}
	fState := *finalState
	signedParts := getSigsOfState(*initialState, initialStateBuf)

	for key := range *initialState {
		if !strings.HasPrefix(key, "_sigs/") || key == "#spec" {
			continue
		}
		_, ok := fState[key]
		if ok {
			continue
		}

		signature := (*initialState)[key].(map[string]interface{})
		deleted, err = DeleteSignatureFromState(*initialState, initialStateBuf, key, signature, signedParts)
		if err != nil {
			return err
		}
	}

	for _, key := range deleted {
		delete(*initialState, key)
		delete(*finalState, key)
	}

	return nil
}

// CleanPatchSignedPkg cleans the patched signed package by removing unnecessary
// keys and expressions. It takes in the source, patch, and target PvrMaps as
// parameters and returns an error if any.
//
// Parameters:
// - src: The source PvrMap.
// - patch: The patch PvrMap.
// - target: The target PvrMap.
//
// Returns:
// - error: An error if any occurred during the cleaning process.
func CleanPatchSignedPkg(src, patch, target *PvrMap) (err error) {
	deleted := []string{}
	srcBuf, err := pvjson.Marshal(*src, pvjson.MarshalOptions{Compact: true})
	if err != nil {
		return err
	}

	srcSigParts := getSigsOfState(*src, srcBuf)
	for key, value := range *patch {
		if !strings.HasPrefix(key, "_sigs/") || key == "#spec" {
			continue
		}
		var previousValue interface{}
		if val, ok := (*src)[key]; ok {
			previousValue = val
		}

		if value == nil && previousValue == nil {
			deleted = append(deleted, key)
			continue
		}

		if value == nil && previousValue != nil {
			del, err := DeleteSignatureFromState(*src, srcBuf, key, previousValue.(map[string]interface{}), srcSigParts)
			if err != nil {
				return err
			}

			deleted = append(deleted, del...)
			for _, d := range del {
				delete(*patch, d)

			}
		} else {
			del, err := DeleteSignatureFromState(*src, srcBuf, key, value.(map[string]interface{}), srcSigParts)
			deleted = append(deleted, del...)

			if err != nil {
				return err
			}
		}
	}

	for _, del := range deleted {
		delete(*src, del)
	}

	return nil
}

func appendToSignedExpression(signedExpresions []*regexp.Regexp, key string, value interface{}) ([]*regexp.Regexp, error) {
	var tmpDecoded PvrMap

	signature := value.(map[string]interface{})
	protected, ok := signature["protected"]
	if !ok {
		return signedExpresions, nil
	}
	if _, ok = signature["signature"]; !ok {
		return signedExpresions, nil
	}

	r, err := convertGlobToRegex(key)
	if err != nil {
		return signedExpresions, err
	}
	signedExpresions = append(signedExpresions, r)

	decodedBuff, err := base64.RawStdEncoding.DecodeString(protected.(string))
	if err != nil {
		return signedExpresions, err
	}

	err = pvjson.Unmarshal(decodedBuff, &tmpDecoded)
	if err != nil {
		return signedExpresions, err
	}

	pvs := tmpDecoded["pvs"].(map[string]interface{})
	include := pvs["include"].([]interface{})
	exclude := pvs["exclude"].([]interface{})

	for _, element := range append(include, exclude...) {
		r, err := convertGlobToRegex(element.(string))
		if err != nil {
			return signedExpresions, err
		}
		signedExpresions = append(signedExpresions, r)
	}

	return signedExpresions, nil
}

func someValid(regexs []*regexp.Regexp, comparable string) bool {
	for _, r := range regexs {
		if r.MatchString(comparable) {
			return true
		}
	}

	return false
}

func convertGlobToRegex(element string) (*regexp.Regexp, error) {
	regexS := strings.ReplaceAll(
		strings.ReplaceAll(
			element,
			"**", ".*",
		),
		"/*", "[^/]*",
	)

	return regexp.Compile("^" + regexS)
}

type SignedParts map[string]map[string]bool

// getSigsOfState retrieves the signed parts of the state based on the provided PVR map and state buffer.
//
// Parameters:
// - state: the PVR state map.
// - stateBuf: the state buffer.
// Return type: SignedParts.
func getSigsOfState(state PvrMap, stateBuf []byte) SignedParts {
	signedParts := make(map[string]map[string]bool)
	for sigKey := range state {
		if !strings.HasPrefix(sigKey, "_sigs/") {
			continue
		}

		summary, err := getSigSummary(stateBuf, state, state[sigKey].(map[string]interface{}))
		if err != nil {
			fmt.Printf("%s\n", err)
			continue
		}

		for _, part := range summary.Protected {
			if _, ok := signedParts[part]; !ok {
				signedParts[part] = make(map[string]bool)
			}
			signedParts[part][sigKey] = true
		}
	}

	return signedParts
}

// DeleteSignatureFromState deletes a signature and signed files from the state and updates the signed parts accordingly.
//
// Parameters:
// - state: the PVR state map.
// - stateBuf: the state buffer.
// - deletedSignature: the signature to be deleted.
// - signedParts: the signed parts mapping.
// Returns a slice of deleted parts and an error.
func DeleteSignatureFromState(state PvrMap, stateBuf []byte, delSigKey string, delSigValue map[string]interface{}, signedParts SignedParts) ([]string, error) {
	if delSigValue == nil {
		return nil, nil
	}

	deletedParts := []string{}
	if signedParts == nil {
		signedParts = getSigsOfState(state, stateBuf)
	}

	_, okSpec := delSigValue["#spec"]
	_, signature := delSigValue["signature"]
	prevSignature, existsPrevSig := state[delSigKey]
	var sigSummary *JwsVerifySummary = nil
	var err error

	// if the spec doesn't exist but the signature does
	// we need to get the summary from the signature from the state
	// Because is an update in the signature
	if okSpec && signature && !existsPrevSig {
		sigSummary, err = getSigSummary(stateBuf, state, delSigValue)
	}

	if sigSummary == nil && strings.Contains(delSigKey, "_sigs/") && existsPrevSig {
		sigSummary, err = getSigSummary(stateBuf, state, prevSignature.(map[string]interface{}))
	}

	if sigSummary == nil && err == nil {
		return nil, nil
	}

	// If any the getSigSummary return an error
	if err != nil {
		return nil, err
	}

	partsOfSignature := append(sigSummary.Protected, sigSummary.Excluded...)
	for path := range state {
		for _, part := range partsOfSignature {
			delete(signedParts[part], delSigKey)
			if part == path && len(signedParts[path]) == 0 {
				delete(state, path)
				deletedParts = append(deletedParts, path)
			}
		}
	}

	return deletedParts, nil
}
