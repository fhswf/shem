#!/bin/bash
set -e

REGISTRY="$1"     # e.g., quay.io/shem
IMAGE_NAME="$2"   # e.g., shem_testmodule
VERSION="$3"      # e.g., 0.0.1
ARCH="$4"         # e.g., amd64
KEY_FILE="$5"     # e.g., signing-key.pem

LOCAL_IMAGE="localhost/${IMAGE_NAME}:${VERSION}-${ARCH}"
REGISTRY_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}-${ARCH}"
SIGNATURE_IMAGE="${REGISTRY}/${IMAGE_NAME}-sig:${VERSION}-${ARCH}"
SIGNATURE_LATEST="${REGISTRY}/${IMAGE_NAME}-sig:latest-${ARCH}"

# Push and capture locally computed digest
DIGEST_FILE=$(mktemp)
echo "Pushing $LOCAL_IMAGE to registry..."
podman push "$LOCAL_IMAGE" "$REGISTRY_IMAGE" --digestfile "$DIGEST_FILE"

DIGEST=$(cat "$DIGEST_FILE")
rm "$DIGEST_FILE"

if [ -z "$DIGEST" ]; then
    echo "ERROR: Could not get digest from podman push"
    exit 1
fi

echo "Locally computed digest: $DIGEST"

# Sign the message
MESSAGE="${REGISTRY_IMAGE} ${DIGEST}"

MSGFILE=$(mktemp)
SIGFILE=$(mktemp)
echo -n "$MESSAGE" > "$MSGFILE"
openssl pkeyutl -sign -inkey "$KEY_FILE" -rawin -in "$MSGFILE" -out "$SIGFILE"
SIGNATURE=$(base64 -w0 < "$SIGFILE")
rm "$MSGFILE" "$SIGFILE"

# Get public key
PUBKEY=$(openssl pkey -in "$KEY_FILE" -pubout -outform DER | tail -c 32 | base64 -w0)

# Create signature container
echo "Creating and pushing signature container ${SIGNATURE_IMAGE}"

cat > Containerfile.sig <<EOF
FROM scratch
LABEL org.opencontainers.image.version="$VERSION"
LABEL energy.shem.registryimage="$REGISTRY_IMAGE"
LABEL energy.shem.digest="$DIGEST"
LABEL energy.shem.pubkey="$PUBKEY"
LABEL energy.shem.signature="$SIGNATURE"
EOF

podman build -f Containerfile.sig -t "$SIGNATURE_IMAGE" .
rm Containerfile.sig

podman push "$SIGNATURE_IMAGE"

# create latest-[arch] tag
echo "Creating tag ${SIGNATURE_LATEST}"
podman tag "$SIGNATURE_IMAGE" "${SIGNATURE_LATEST}"
podman push "${SIGNATURE_LATEST}"
