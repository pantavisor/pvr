// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package repoutil provides repository-reference normalisation helpers.
// Extracted from libpvr to avoid pulling the docker SDK for consumers that
// only need URL/reference manipulation.
package repoutil

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// FixupRepoRef normalises a pvr repository reference. See libpvr.FixupRepoRef.
func FixupRepoRef(repoUri string) (string, error) {
	var pathExists bool
	var uri *url.URL
	var err error

	fixedUri := ""
	poundSuffix := ""

	poundParts := strings.Split(repoUri, "#")

	fixedUri += poundParts[0]
	if net.ParseIP(fixedUri) != nil {
		fixedUri = "http://" + fixedUri + ":12368/cgi-bin/pvr"
	}

	if len(poundParts[1:]) == 0 {
		goto skip
	}

	poundSuffix += "#"
	poundSuffix += strings.ReplaceAll(poundParts[1], "/", "%2F")
	if len(poundParts[2:]) == 0 {
		goto skip
	}
	for _, v := range poundParts[2:] {
		poundSuffix += "%23"
		poundSuffix += strings.ReplaceAll(v, "/", "%2F")
	}

skip:

	fixedUri += poundSuffix
	uri, err = url.Parse(fixedUri)
	if err != nil {
		return "", err
	}

	pathExists, err = isFileExists(uri.Path)
	if err != nil {
		return "", err
	}

	if !isValidUrl(fixedUri) && !pathExists {
		userNick := ""
		deviceNick := ""
		splits := strings.Split(repoUri, "/")
		if len(splits) == 1 {
			return "", errors.New("device nick is missing. (syntax:pvr get <USER_NICK>/<DEVICE_NICK>[#<part>]). See --help")
		} else if len(splits) == 2 {
			userNick = splits[0]
			deviceNick = splits[1]
		} else {
			return "", errors.New("clone URL '" + uri.Path + "' is not a valid URL, nor in pvr device ref format; see --help")
		}
		fixedUri = "https://pvr.pantahub.com/" + userNick + "/" + deviceNick
	}

	return fixedUri, nil
}

func isFileExists(filePath string) (bool, error) {
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func isValidUrl(value string) bool {
	_, err := url.ParseRequestURI(value)
	if err != nil {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return true
}
