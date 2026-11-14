#!/bin/sh
#
# verify-plain-manifest.sh <project-path> <tag> [<tag> ...]
#
# Fails the CI job if any of the given tags in the container registry is
# NOT a plain Docker v2 image manifest. Works against GitLab (default) and
# GitHub (REGISTRY=ghcr.io).
#
# Why this exists: `pvr self-upgrade` on already-deployed clients uses
# libpvr/dockerlib_legacy.go, which requests ONLY
# "application/vnd.docker.distribution.manifest.v2+json". When buildkit wraps a
# pushed image in an OCI image index (provenance/attestation), the registry has
# no plain manifest to hand back and returns 404 -> "image not found or you do
# not have access", silently bricking self-upgrade for every existing install.
#
# This guard requests each channel tag EXACTLY as an old pvr client does and
# fails if the response isn't a plain v2 manifest, so such an image can never
# be shipped again. The CI configs set BUILDX_NO_DEFAULT_ATTESTATIONS=1 to
# prevent it; this is the belt-and-suspenders check that it actually worked.
#
set -eu

# REGISTRY=ghcr.io needs REGISTRY_USER and REGISTRY_TOKEN (a token that can read the package).
REGISTRY="${REGISTRY:-registry.gitlab.com}"
PLAIN="application/vnd.docker.distribution.manifest.v2+json"

if [ "$#" -lt 2 ]; then
	echo "usage: $0 <project-path> <tag> [<tag> ...]" >&2
	exit 2
fi

REPO="$1"
shift

# Pull token scoped to this repository.
if [ "$REGISTRY" = "ghcr.io" ]; then
	TOKEN=$(curl -sf -u "${REGISTRY_USER:-token}:${REGISTRY_TOKEN:-}" \
		"https://ghcr.io/token?service=ghcr.io&scope=repository:${REPO}:pull" \
		| sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
elif [ -n "${CI_JOB_TOKEN:-}" ]; then
	TOKEN=$(curl -sf -u "gitlab-ci-token:${CI_JOB_TOKEN}" \
		"https://gitlab.com/jwt/auth?service=container_registry&scope=repository:${REPO}:pull" \
		| sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
else
	TOKEN=$(curl -sf \
		"https://gitlab.com/jwt/auth?service=container_registry&scope=repository:${REPO}:pull" \
		| sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
fi

if [ -z "${TOKEN:-}" ]; then
	echo "ERROR: could not obtain a registry pull token for ${REPO}" >&2
	exit 1
fi

rc=0
for tag in "$@"; do
	# Ask for the manifest with ONLY the plain v2 media type, exactly as an old
	# pvr client does. %{http_code} + %{content_type} tells us what it gets.
	resp=$(curl -s -o /dev/null -w '%{http_code} %{content_type}' \
		-H "Authorization: Bearer ${TOKEN}" \
		-H "Accept: ${PLAIN}" \
		"https://${REGISTRY}/v2/${REPO}/manifests/${tag}")
	code=${resp%% *}
	ctype=${resp#* }
	# content_type may carry parameters (e.g. "; charset=..."); strip them.
	ctype=${ctype%%;*}
	ctype=${ctype% }

	if [ "$code" = "200" ] && [ "$ctype" = "$PLAIN" ]; then
		echo "OK:   ${REPO}:${tag} is a plain v2 manifest (old pvr self-upgrade works)"
	else
		echo "FAIL: ${REPO}:${tag} is NOT a plain v2 manifest (http=${code}, type=${ctype:-none})." >&2
		echo "      An old pvr self-upgrade would get 'image not found' here." >&2
		echo "      Likely a buildkit attestation/OCI index; ensure" >&2
		echo "      BUILDX_NO_DEFAULT_ATTESTATIONS=1 and a plain 'docker build' + 'docker push'." >&2
		rc=1
	fi
done

exit $rc
