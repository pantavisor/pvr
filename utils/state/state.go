// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package state provides state-patching helpers that operate on Pantavisor
// JSON state documents. It is extracted from libpvr so that consumers who only
// need state manipulation do not transitively import the docker SDK that
// libpvr's image-handling code depends on.
package state

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	jsonpatch "github.com/asac/json-patch"
	cjson "github.com/gibson042/canonicaljson-go"
	gojose "github.com/go-jose/go-jose/v3"
	"github.com/highercomve/jsondiff"
	"gitlab.com/pantacor/pvr/utils/pvjson"

	"github.com/bmatcuk/doublestar"
)

// PvrMap is the canonical state map representation.
type PvrMap = map[string]interface{}

// PvsMatch describes which paths a signature covers.
type PvsMatch struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// PvsPartSelection groups state keys by their relation to a PvsMatch.
type PvsPartSelection struct {
	Selected    map[string]interface{}
	NotSelected map[string]interface{}
	NotSeen     map[string]interface{}
}

// JwsVerifySummary summarises which paths a JWS signature protects.
type JwsVerifySummary struct {
	Protected       []string      `json:"protected,omitempty"`
	Excluded        []string      `json:"excluded,omitempty"`
	NotSeen         []string      `json:"notseen,omitempty"`
	FullJSONWebSigs []interface{} `json:"sigs,omitempty"`
}

// SignedParts maps a protected path to the set of signature keys covering it.
type SignedParts map[string]map[string]bool

func copyState(state map[string]interface{}) map[string]interface{} {
	data, err := json.Marshal(state)
	if err != nil {
		return state
	}
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	if err != nil {
		return state
	}
	return result
}

// AddFragsToState merges patchState into srcState honouring fragment scopes.
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

// ExpandFragmentsWithSignedParts expands fragment selectors to include any
// signed parts referenced by sigs in src.
func ExpandFragmentsWithSignedParts(src PvrMap, fragments string) (string, error) {
	result := strings.Split(fragments, ",")
	srcBuf, err := json.Marshal(src)
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

// FilterByFrags returns the subset of state matching the fragment selectors.
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
		for k := range pJSONMap {
			if strings.HasPrefix(k, partPrefix) {
				delete(pJSONMap, k)
			}
		}

		for k, v := range state {
			if strings.HasPrefix(k, partPrefix) {
				pJSONMap[k] = v
			}
		}
	}

	for _, unpartPrefix := range unpartPrefixes {
		for k := range pJSONMap {
			if strings.HasPrefix(k, unpartPrefix) {
				delete(pJSONMap, k)
			}
		}
	}

	return pJSONMap, nil
}

// OverwriteState replaces the contents of state with newState in place.
func OverwriteState(state PvrMap, newState PvrMap) {
	for key := range state {
		delete(state, key)
	}
	for key, value := range newState {
		state[key] = value
	}
}

// PatchState applies patchBuff to srcBuff respecting fragment scopes and
// signature handling.
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

	for key, value := range patchState {
		if value == nil {
			delete(patchState, key)
		}
	}

	jsonMerged, err = cjson.Marshal(&patchState)
	if err != nil {
		return nil, nil, err
	}

	return jsonMerged, patchState, err
}

// GetStatePatch returns the JSON-patch diff between initialState and endState.
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

// MergePatch produces target from srcData honouring signed-package rules.
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

// CleanRemovedSignatures removes orphan signed paths between two states.
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

// CleanPatchSignedPkg removes signed-package leftovers from src/patch/target.
func CleanPatchSignedPkg(src, patch, target *PvrMap) (err error) {
	deleted := []string{}
	srcBuf, err := json.Marshal(*src)
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

// DeleteSignatureFromState removes a signature and its protected paths from state.
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

	if okSpec && signature && !existsPrevSig {
		sigSummary, err = getSigSummary(stateBuf, state, delSigValue)
	}

	if sigSummary == nil && strings.Contains(delSigKey, "_sigs/") && existsPrevSig {
		sigSummary, err = getSigSummary(stateBuf, state, prevSignature.(map[string]interface{}))
	}

	if sigSummary == nil && err == nil {
		return nil, nil
	}

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

// --- minimal subset of pvslib needed by PatchState ---------------------------

func selectPayload(stateBuf []byte, match *PvsMatch, stateMap PvrMap) (*PvsPartSelection, error) {
	selection := PvsPartSelection{
		Selected:    map[string]interface{}{},
		NotSelected: map[string]interface{}{},
		NotSeen:     map[string]interface{}{},
	}

	if stateMap == nil {
		stateMap = map[string]interface{}{}
		err := pvjson.Unmarshal(stateBuf, &stateMap)
		if err != nil {
			return nil, err
		}
	}

	for k, v := range stateMap {
		var found interface{}
		key := ""
		var excludedMatch bool

		for _, I := range match.Include {
			m, err := doublestar.Match(I, k)
			if err != nil {
				return nil, err
			}
			if !m {
				continue
			}

			found = v
			key = k
			break
		}

		excludedMatch = false
		for _, E := range match.Exclude {
			m, err := doublestar.Match(E, k)
			if err != nil {
				return nil, err
			}
			if !m {
				continue
			}
			found = nil
			key = ""
			excludedMatch = true
			break
		}

		if key != "" {
			selection.Selected[key] = found
		} else if excludedMatch {
			selection.NotSelected[k] = v
		} else {
			selection.NotSeen[k] = v
		}
	}

	return &selection, nil
}

func getSigMatch(signature PvrMap) (*PvsMatch, error) {
	if _, ok := signature["#spec"]; !ok {
		return nil, errors.New("ERROR: pvs signature does not have a #spec element")
	}

	if signature["#spec"].(string) != "pvs@2" {
		return nil, errors.New("ERROR: pvs signature must be of #spec pvs@2")
	}

	buf, err := pvjson.Marshal(signature)
	if err != nil {
		return nil, err
	}

	sig, err := gojose.ParseSigned(string(buf))
	if err != nil {
		return nil, err
	}

	header := sig.Signatures[0].Protected
	pvsHeader := header.ExtraHeaders[gojose.HeaderKey("pvs")]

	jsonBuf, err := json.Marshal(pvsHeader)
	if err != nil {
		return nil, err
	}

	var match *PvsMatch
	err = pvjson.Unmarshal(jsonBuf, &match)
	if err != nil {
		return nil, err
	}
	return match, nil
}

func getSigSummary(stateBuf []byte, stateMap PvrMap, signature PvrMap) (*JwsVerifySummary, error) {
	match, err := getSigMatch(signature)
	if err != nil {
		return nil, err
	}

	selection, err := selectPayload(stateBuf, match, stateMap)
	if err != nil {
		return nil, err
	}

	summary := &JwsVerifySummary{
		Protected: []string{},
		Excluded:  []string{},
		NotSeen:   []string{},
	}

	for k := range selection.Selected {
		summary.Protected = append(summary.Protected, k)
	}
	for k := range selection.NotSelected {
		summary.Excluded = append(summary.Excluded, k)
	}
	for k := range selection.NotSeen {
		summary.NotSeen = append(summary.NotSeen, k)
	}

	return summary, nil
}

// silence unused-helper lint complaints from selectively-copied code.
var (
	_ = appendToSignedExpression
	_ = someValid
)
