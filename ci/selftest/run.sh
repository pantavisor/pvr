#!/bin/sh
#
# run.sh -- install + self-upgrade smoke test in a clean container.
#
# Builds a bare Ubuntu image (curl/tar/gzip only, like a fresh user machine)
# and exercises:
#   - install.sh for the stable and develop channels
#   - a release client (051) self-upgrading via the docker-registry manifest
#     path (the path that broke when :stable/:develop became attestation
#     indexes -- this is the regression guard)
#   - a master build self-upgrading via the generic-package path
#   - install.sh from GitHub releases (059 and main-snapshot)
#
# Requires: docker, network access to gitlab.com and github.com. Runs against the LIVE
# registry/packages, so it also confirms the published channels are healthy.
#
# Usage:  ci/selftest/run.sh
#
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"
image="pvr-selftest"

echo ">>> building $image"
docker build -q -t "$image" "$here" >/dev/null

echo ">>> running install + self-upgrade tests"
exec docker run --rm \
	-v "$repo_root/install.sh:/work/install.sh:ro" \
	-v "$here/intest.sh:/work/intest.sh:ro" \
	"$image" bash /work/intest.sh
