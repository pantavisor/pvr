#!/bin/sh
#
# republish-plain-manifest.sh <tag> [<tag> ...]
#
# One-shot repair for channel tags that were pushed as a buildkit OCI image
# index (provenance/attestation) instead of a plain Docker v2 manifest, which
# breaks `pvr self-upgrade` for already-deployed clients (see
# ci/verify-plain-manifest.sh for the why).
#
# It rewrites the tag to point at a single plain **docker v2s2** manifest for
# the linux/amd64 image (the pvr image is amd64-only and bundles every
# cross-built binary as files), matching the format of the known-good
# :latest / :master tags. Same bits, no rebuild, reversible.
#
# NOTE: plain `docker pull` + `docker push` does NOT work for this on a Docker
# engine using the containerd image store (Docker 24+ / Docker Desktop): it
# preserves the OCI index and re-pushes it byte-for-byte. We use `skopeo`,
# which converts to v2s2 and selects the single amd64 image.
#
# Requires: skopeo, and registry credentials with push rights. skopeo reads
# ~/.docker/config.json by default, so a prior `docker login registry.gitlab.com`
# is enough.
#
#   Alternative (recommended): just re-run the CI pipeline for the affected
#   channel -- the tag pipeline (build-tags) for :stable, the develop pipeline
#   (build) for :develop. Those push a v2s2 manifest via dind and are now
#   verified by ci/verify-plain-manifest.sh.
#
set -eu

IMAGE="registry.gitlab.com/pantacor/pvr"

if [ "$#" -lt 1 ]; then
	echo "usage: $0 <tag> [<tag> ...]" >&2
	exit 2
fi

if ! command -v skopeo >/dev/null 2>&1; then
	echo "ERROR: skopeo is required (docker pull/push cannot down-convert an" >&2
	echo "       OCI index on the containerd image store). Install skopeo, or" >&2
	echo "       re-run the CI pipeline for the affected channel instead." >&2
	exit 1
fi

for tag in "$@"; do
	echo ">>> re-publishing ${IMAGE}:${tag} as a plain docker v2s2 manifest"
	# --multi-arch system + amd64 overrides: pick the single linux/amd64 image
	# out of the index (never the unknown/unknown attestation child).
	# --format v2s2: emit a plain docker manifest, matching :latest/:master.
	skopeo copy \
		--multi-arch system \
		--override-os linux \
		--override-arch amd64 \
		--format v2s2 \
		"docker://${IMAGE}:${tag}" \
		"docker://${IMAGE}:${tag}"
done

echo ">>> done -- verify with: sh ci/verify-plain-manifest.sh pantacor/pvr $*"
