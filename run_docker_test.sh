#!/bin/sh

# This script builds and runs the tests inside a Docker container.
#
# Usage:
#   ./run_docker_test.sh
#
# Requirements:
#   - Docker must be installed and running
#   - Access to /var/run/docker.sock

set -e

DOCKER_CONFIG_MOUNT=""
if [ -d "$HOME/.docker" ]; then
    DOCKER_CONFIG_MOUNT="-v $HOME/.docker:/root/.docker:ro"
fi

docker build -f Dockerfile.test -t pvr-test .
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock $DOCKER_CONFIG_MOUNT pvr-test
