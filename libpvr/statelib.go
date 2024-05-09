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
	"regexp"
	"strings"

	jsonpatch "github.com/asac/json-patch"
	"github.com/bmatcuk/doublestar"
	cjson "github.com/gibson042/canonicaljson-go"
	"github.com/highercomve/jsondiff"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

func copyState(state map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range state {
		result[k] = v
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

	err := cjson.Unmarshal(srcBuff, &srcState)
	if err != nil {
		return nil, nil, err
	}

	err = cjson.Unmarshal(patchBuff, &patchState)
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

		srcJsonMap, err := cjson.Marshal(pJSONMap)
		if err != nil {
			return nil, nil, err
		}

		jsonDataSelect, err := cjson.Marshal(patchState)
		if err != nil {
			return nil, nil, err
		}

		jsonMerged, err = jsonpatch.MergePatch(srcJsonMap, jsonDataSelect)
		if err != nil {
			return nil, nil, err
		}
	} else {
		jsonMap := AddFragsToState(srcState, patchState, srcFrags)
		jsonMerged, err = cjson.Marshal(jsonMap)
		if err != nil {
			return nil, nil, err
		}
	}

	if _, ok := patchState["#spec"]; !ok {
		patchState["#spec"] = "pantavisor-service-system@1"
	}

	err = cjson.Unmarshal(jsonMerged, &patchState)
	if err != nil {
		return nil, nil, err
	}

	if state != nil {
		OverwriteState(*state, patchState)
	}

	jsonMerged, err = cjson.Marshal(&patchState)

	return jsonMerged, patchState, err
}

func GetStatePatch(initialState, endState []byte) (*[]byte, error) {
	var diffbytes []byte
	diffJson := []interface{}{}
	diff, err := jsondiff.CompareJSON(initialState, endState)
	if err != nil {
		return nil, err
	}

	if diff != nil {
		diffbytes, err = cjson.Marshal(diff)
		if err != nil {
			return nil, err
		}
		err = pvjson.Unmarshal(diffbytes, &diffJson)
		if err != nil {
			return nil, err
		}
	}

	return &diffbytes, err
}

// DeleteSignatureFromState deletes a signature from the initial state based on the deleted signature.
//
// Parameters:
// - initialState: The initial state containing the signatures.
// - deletedSignature: The signature to be deleted from the state.
// Return type: error
func DeleteSignatureFromState(initialState PvrMap, deletedSignatures []string) ([]string, error) {

	signedParts := make(map[string]map[string]bool)
	for sigPath := range initialState {
		if !strings.HasPrefix(sigPath, "_sigs/") {
			continue
		}

		sigMap := map[string]interface{}{}
		sigBytes, err := base64.StdEncoding.DecodeString(initialState[sigPath].(map[string]interface{})["protected"].(string))
		if err != nil {
			return nil, err
		}
		err = cjson.Unmarshal(sigBytes, &sigMap)
		if err != nil {
			return nil, err
		}

		includeArr := sigMap["pvs"].(map[string]interface{})["include"].([]interface{})

		for path := range initialState {
			for _, includeGlob := range includeArr {
				glob, ok := includeGlob.(string)
				if !ok {
					continue
				}

				if match, err := doublestar.Match(glob, path); err == nil && match {
					if _, ok := signedParts[path]; !ok {
						signedParts[path] = map[string]bool{}
					}
					signedParts[path][sigPath] = true
				}
			}
		}
	}

	deletedParts := []string{}

	for _, deletedSignature := range deletedSignatures {

		delSigProtected, err := base64.StdEncoding.DecodeString(initialState[deletedSignature].(map[string]interface{})["protected"].(string))
		if err != nil {
			return nil, err
		}

		delSigMap := map[string]interface{}{}
		err = cjson.Unmarshal(delSigProtected, &delSigMap)
		if err != nil {
			return nil, err
		}

		partsOfSignature := []interface{}{}
		if val, ok := delSigMap["pvs"].(map[string]interface{})["include"]; ok {
			partsOfSignature = append(partsOfSignature, val.([]interface{})...)
		}

		if val, ok := delSigMap["pvs"].(map[string]interface{})["exclude"]; ok {
			partsOfSignature = append(partsOfSignature, val.([]interface{})...)
		}

		for path := range initialState {
			for _, part := range partsOfSignature {
				p, ok := part.(string)
				if !ok {
					continue
				}

				delete(signedParts[path], deletedSignature)
				if match, err := doublestar.Match(p, path); err == nil && match && len(signedParts[path]) == 0 {
					delete(initialState, path)
					deletedParts = append(deletedParts, path)
				}
			}
		}
	}

	return deletedParts, nil
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

	err = cjson.Unmarshal(srcData, &srcState)
	if err != nil {
		return nil, err
	}

	err = cjson.Unmarshal(target, &targetState)
	if err != nil {
		return nil, err
	}

	err = cjson.Unmarshal(patchData, &patchState)
	if err != nil {
		return nil, err
	}

	err = CleanPatchSignedPkg(&srcState, &patchState, &targetState)
	if err != nil {
		return nil, err
	}

	srcData, err = cjson.Marshal(srcState)
	if err != nil {
		return nil, err
	}

	target, err = cjson.Marshal(targetState)
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
func CleanRemovedSignatures(initialState, finalState *PvrMap) error {
	var err error
	signedExpresions := []*regexp.Regexp{}
	fState := *finalState

	for key, value := range *initialState {
		if !strings.HasPrefix(key, "_sigs/") || key == "#spec" {
			continue
		}
		_, ok := fState[key]
		if ok {
			continue
		}

		signedExpresions, err = appendToSignedExpression(signedExpresions, key, value)
		if err != nil {
			return err
		}
	}

	if len(signedExpresions) == 0 {
		return nil
	}

	for key := range *initialState {
		if someValid(signedExpresions, key) && key != "#spec" {
			delete(*initialState, key)
		}
	}

	for key := range *finalState {
		if someValid(signedExpresions, key) && key != "#spec" {
			delete(*finalState, key)
		}
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
func CleanPatchSignedPkg(src, patch, target *PvrMap) error {
	var err error
	signedSrcExpresions := map[string][]*regexp.Regexp{}
	signedPatchExpresions := map[string][]*regexp.Regexp{}
	srcJson := *src
	packages := map[string]bool{}
	ok := false

	for key, value := range *patch {
		if !strings.HasPrefix(key, "_sigs/") || key == "#spec" {
			continue
		}

		packageName := strings.ReplaceAll(strings.ReplaceAll(key, "_sigs/", ""), ".json", "")

		packages[packageName] = false
		if _, ok := signedSrcExpresions[packageName]; !ok {
			signedSrcExpresions[packageName] = []*regexp.Regexp{}
		}

		if _, ok := signedPatchExpresions[packageName]; !ok {
			signedPatchExpresions[packageName] = []*regexp.Regexp{}
		}

		if value == nil {
			value, ok = srcJson[key]
			if !ok {
				continue
			}

			signedSrcExpresions[packageName], err = appendToSignedExpression(signedPatchExpresions[packageName], key, value)
			if err != nil {
				return err
			}
		}

		signedPatchExpresions[packageName], err = appendToSignedExpression(signedSrcExpresions[packageName], key, value)
		if err != nil {
			return err
		}
	}

	if len(signedSrcExpresions) == 0 && len(signedPatchExpresions) == 0 {
		return nil
	}

	for key := range *patch {
		for p, exp := range signedPatchExpresions {
			if someValid(exp, key) && !strings.Contains(key, "_sigs/") {
				packages[p] = true
			}
		}

		for _, exp := range signedSrcExpresions {
			if someValid(exp, key) && key != "#spec" {
				delete(*patch, key)
			}
		}
	}

	for key := range *src {
		for p, exp := range signedPatchExpresions {
			if someValid(exp, key) && packages[p] && key != "#spec" {
				delete(*src, key)
			}
		}
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

	err = cjson.Unmarshal(decodedBuff, &tmpDecoded)
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
