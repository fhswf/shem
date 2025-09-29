#!/bin/bash
set -e

IMAGE_NAME="shem-orchestrator"
VERSION="$1"      # e.g., 0.0.1
ARCH="$2"         # e.g., amd64

CONTAINER=tmp-shem-extract

# extract orchestrator binary from local image
podman create --replace --name "${CONTAINER}" "localhost/${IMAGE_NAME}:${VERSION}-${ARCH}"
podman cp "${CONTAINER}:/shem-orchestrator" "./shem/bin/shem-orchestrator-${VERSION}"
podman rm tmp-shem-extract

# create symlink and build tarfile
ln -s "shem-orchestrator-${VERSION}" "./shem/bin/shem-orchestrator"
tar czf "shem-release-${VERSION}-${ARCH}.tar.gz" shem

rm "./shem/bin/shem-orchestrator-${VERSION}"
rm "./shem/bin/shem-orchestrator"
