#!/bin/sh

# This script simulates the GitLab CI environment locally by running
# Docker-in-Docker (DinD) and executing tests with DOCKER_HOST over TCP.
#
# Usage:
#   ./run_docker_test_dind.sh

set -e

DIND_CONTAINER_NAME="pvr-test-dind"
TEST_IMAGE="pvr-test"

# Cleanup function
cleanup() {
    echo "Cleaning up DinD container..."
    docker rm -f "$DIND_CONTAINER_NAME" 2>/dev/null || true
}

# Set trap to cleanup on exit
trap cleanup EXIT

# Remove any existing DinD container
docker rm -f "$DIND_CONTAINER_NAME" 2>/dev/null || true

echo "Starting Docker-in-Docker container..."
docker run -d --privileged \
    --name "$DIND_CONTAINER_NAME" \
    -e DOCKER_TLS_CERTDIR="" \
    -p 2375:2375 \
    docker:27.3.1-dind

echo "Waiting for DinD to be ready..."
sleep 5

# Wait for Docker daemon to be available
for i in $(seq 1 30); do
    if docker exec "$DIND_CONTAINER_NAME" docker info >/dev/null 2>&1; then
        echo "DinD is ready!"
        break
    fi
    echo "Waiting for DinD... ($i/30)"
    sleep 2
done

# Verify DinD is working
if ! docker exec "$DIND_CONTAINER_NAME" docker info >/dev/null 2>&1; then
    echo "ERROR: DinD failed to start"
    exit 1
fi

echo "Building test image..."
docker build -f Dockerfile.test -t "$TEST_IMAGE" .

echo "Logging into GitLab registry in DinD (if credentials available)..."
if [ -f "$HOME/.docker/config.json" ]; then
    # Copy docker config to DinD for registry authentication
    docker cp "$HOME/.docker/config.json" "$DIND_CONTAINER_NAME:/root/.docker/config.json" 2>/dev/null || true
fi

echo ""
echo "Running tests with DOCKER_HOST=tcp://127.0.0.1:2375 (simulating CI)..."
echo "=================================================================="
echo ""

# Run the test container similar to CI
docker run --rm \
    --network host \
    -e DOCKER_HOST=tcp://127.0.0.1:2375 \
    -v "$HOME/.docker:/root/.docker:ro" \
    "$TEST_IMAGE"

echo ""
echo "Tests completed!"
