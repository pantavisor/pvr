#!/bin/sh
#
# republish-plain-manifest.sh <tag> [<tag> ...]
#
# One-shot repair for channel tags that were pushed as a buildkit OCI image
# index (provenance/attestation) instead of a plain Docker v2 manifest, which
# breaks `pvr self-upgrade` for already-deployed clients (see
# ci/verify-plain-manifest.sh for the why).
#
# It pulls the existing image (docker selects the real platform image out of
# the index and stores it locally as a plain image) and pushes it straight
# back. The docker image store cannot hold an attestation index, so the
# re-push emits a plain v2 manifest pointing at the SAME binaries -- no rebuild,
# fully reversible. Requires `docker login registry.gitlab.com` with push
# rights to the repo.
#
set -eu

IMAGE="registry.gitlab.com/pantacor/pvr"
PLATFORM="linux/amd64" # the pvr image is a single amd64 alpine image that bundles all cross-built binaries

if [ "$#" -lt 1 ]; then
	echo "usage: $0 <tag> [<tag> ...]" >&2
	exit 2
fi

for tag in "$@"; do
	echo ">>> re-publishing ${IMAGE}:${tag} as a plain manifest"
	docker pull --platform "$PLATFORM" "${IMAGE}:${tag}"
	docker push "${IMAGE}:${tag}"
done

echo ">>> done"
