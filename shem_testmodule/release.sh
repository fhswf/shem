#!/bin/bash
set -e

REGISTRY="quay.io/shem"
IMAGE_NAME="shem_testmodule"
VERSION="0.0.1"

# Build
echo "Building for amd64..."
./build.sh ${VERSION} "amd64"

echo "Building for arm64..."
./build.sh ${VERSION} "arm64"

# Push to registry
echo "Pushing and signing for amd64..."
./push-and-sign.sh ${REGISTRY} ${IMAGE_NAME} ${VERSION} amd64 ~/sec/shem-signing-key.pem

echo "Pushing and signing for arm64..."
./push-and-sign.sh ${REGISTRY} ${IMAGE_NAME} ${VERSION} arm64 ~/sec/shem-signing-key.pem

echo Done.
