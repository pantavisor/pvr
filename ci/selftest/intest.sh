#!/bin/bash
# Runs INSIDE the clean container. Exercises install.sh and BOTH self-upgrade
# paths for real:
#   - release client (051) -> docker-registry manifest path  (the tags we fixed)
#   - master build         -> generic-package path
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"

PASS=0
FAIL=0
ok() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
ko() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
ver() { pvr version 2>/dev/null | awk '/pvr version/{print $3}'; }
reset_state() { rm -f "$HOME/.local/bin/pvr"; rm -rf "$HOME/.pvr"; hash -r; }

# install a specific version, verify the binary landed, retry once on a
# transient GitLab hiccup so a flaky download doesn't look like a product bug.
install_ver() {
	want="$1"
	for attempt in 1 2 3; do
		reset_state
		PVR_VERSION="$want" sh /work/install.sh >/tmp/inst.log 2>&1 && command -v pvr >/dev/null 2>&1 && return 0
		echo "  (install $want attempt $attempt failed, retrying)"; sleep 3
	done
	echo "  install $want FAILED:"; sed 's/^/    /' /tmp/inst.log; return 1
}

# Run self-upgrade, retrying only on transient network errors (TLS blips,
# resets, EOF on the docker bridge). A manifest error -- the bug we fixed --
# is NOT transient and fails immediately.
run_upgrade() {
	for attempt in 1 2 3 4; do
		out=$(pvr self-upgrade 2>&1); rc=$?
		if echo "$out" | grep -qiE "image not found|do not have access|MANIFEST_UNKNOWN|no child with platform"; then
			return 9 # real manifest failure -> stop, this is the regression
		fi
		[ "$rc" -eq 0 ] && return 0
		if echo "$out" | grep -qiE "bad record MAC|connection reset|unexpected EOF|TLS handshake|i/o timeout|timeout"; then
			echo "  (transient network error, retry $attempt)"; sleep 3; continue
		fi
		return "$rc" # some other non-transient failure
	done
	return 1
}

# Classify which upgrade path ran and pass/fail accordingly.
judge() {
	label="$1"; want="$2"; out="$3"; rc="$4"
	echo "$out" | sed 's/^/      | /'
	if [ "$rc" -eq 9 ]; then ko "$label -> MANIFEST ERROR (the bug we fixed)"; return; fi
	if [ "$rc" -ne 0 ]; then ko "$label (transient network failure after retries, exit $rc)"; return; fi
	if echo "$out" | grep -qiE "layers|Docker"; then got=docker; else got=package; fi
	if [ "$want" = any ] || [ "$got" = "$want" ]; then
		ok "$label (path=$got)"
	else
		ko "$label (expected $want path, got $got)"
	fi
}

echo "################ install.sh: what does each channel resolve to? ################"
sh /work/install.sh 2>&1 | grep -iE "version|arch" | sed 's/^/  stable  /'
reset_state
echo "  -- NOTE: if 'stable' shows a master-* version, that is the install.sh bug --"

echo "################ TEST 1: release 051 = docker-only client, channel STABLE ################"
reset_state
install_ver 051
hash -r
echo "  installed release: $(ver)"
[ "$(ver)" = "051" ] && ok "install.sh PVR_VERSION=051" || ko "expected 051, got $(ver)"
pvr global-config DistributionTag=stable >/dev/null
run_upgrade; rc=$?
judge "051 self-upgrade on :stable" docker "$out" "$rc"

echo "################ TEST 2: release 051 client, channel DEVELOP ################"
reset_state
install_ver 051
hash -r
pvr global-config DistributionTag=develop >/dev/null
run_upgrade; rc=$?
judge "051 self-upgrade on :develop" docker "$out" "$rc"

echo "################ TEST 3: release 051 client, channel LATEST ################"
reset_state
install_ver 051
hash -r
pvr global-config DistributionTag=latest >/dev/null
run_upgrade; rc=$?
judge "051 self-upgrade on :latest" docker "$out" "$rc"

echo "################ TEST 4: master build = generic-package path ################"
reset_state
install_ver master
hash -r
echo "  installed master build: $(ver)"
pvr global-config DistributionTag=stable >/dev/null
run_upgrade; rc=$?
judge "master self-upgrade (package path)" package "$out" "$rc"

echo "===================================================================="
echo "RESULT: PASS=$PASS  FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
