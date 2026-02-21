#!/bin/bash
set -e

IMAGE_NAME="shem-orchestrator"
VERSION="$1"
ARCH="$2"

podman build \
    --platform linux/${ARCH} \
    --build-arg VERSION=${VERSION} \
    -t "${IMAGE_NAME}:${VERSION}-${ARCH}" \
    -f ../Containerfile \
    ../..
