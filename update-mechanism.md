# Updates and Rollback
This document describes how the orchestrator handles updates for both modules and itself.

Updates are published via container registries. The orchestrator downloads the container images, verifies signatures, and updates modules and itself automatically. If a failure is detected, the offending update is rolled back.

## Initial installation
SHEM is installed by extracting an archive in the home directory of the user that SHEM will run as. The archive contains the directory structure, the binary of the orchestrator, the public key for orchestrator updates, and a template for the systemd unit.

```
$SHEM_HOME/                     # default: ~/shem
|-- bin/                        # orchestrator binaries
|-- config/                     # configuration files
|-- pubkeys/                    # publisher public keys
...
|-- shem-orchestrator.service
```

Container images will be stored wherever podman is configured to store them.

## Container registries
Modules, module updates, and orchestrator updates are published via container registries. Tags are used for different versions. For each binary image, an accompanying image is published that contains the signature for the binary image. It is called a_module-sig:vx.y.z for the a_module:vx.y.z image.

For example, the orchestrator images might look like this:
```
quay.io/shem/
|── shem-orchestrator:v0.0.1          # binary containers
|── shem-orchestrator:v0.0.2
|── shem-orchestrator-sig:v0.0.1      # signature containers (labels only)
|── shem-orchestrator-sig:v0.0.2
|── shem-orchestrator-sig:stable      # always contains the most recent stable version
```


## Signature Mechanism
SHEM uses Ed25519 signatures for verifying updates. OpenSSL's pkeyutl can be used to sign releases.

The signature covers the module name, version, and the digest of the binary container. For example, the string "shem-orchestrator:v0.0.1 sha256:3b4c5d6e..." is signed for the orchestrator binary, version 0.0.1.

Both the public key and signature are stored as labels in a special signature container. In this example, the container would be called shem-orchestrator-sig:v0.0.1.

The Containerfile might look like this:
```dockerfile
FROM scratch
LABEL energy.shem.digest="sha256:3b4c5d6e..."
LABEL energy.shem.pubkey1="MCowBQYDK2VwAyEAcQyjQftwIlSGYvWjfDMzpr0B5/Lr/S8jDFfVW3hOBk0="
LABEL energy.shem.signature1="AiMEIX/R..."
```

### Creating Signature Containers
When signing containers, make sure to compute the digest locally (otherwise you would trust the registry to not change the container).
```bash
#!/bin/bash
# sign-container.sh - sign container using locally computed digest

set -e

IMAGE_NAME="$1"   # e.g., shem-orchestrator
VERSION="$2"      # e.g., v0.1.0
KEY_FILE="$3"     # e.g., private.key
KEY_NUMBER="${4:-1}"

LOCAL_IMAGE="localhost/${IMAGE_NAME}:${VERSION}"
REGISTRY_IMAGE="quay.io/shem/${IMAGE_NAME}:${VERSION}"
SIGNATURE_IMAGE="quay.io/shem/${IMAGE_NAME}-sig:${VERSION}"

# Push and capture locally computed digest (do not trust registry by using digest from there)
echo "Pushing to registry..."
PUSH_OUTPUT=$(podman push "$LOCAL_IMAGE" "$REGISTRY_IMAGE" 2>&1)
echo "$PUSH_OUTPUT"

# Extract digest that podman computed locally
DIGEST=$(echo "$PUSH_OUTPUT" | grep -oP 'Writing manifest to image destination.*sha256:\K[a-f0-9]{64}')

if [ -z "$DIGEST" ]; then
    echo "ERROR: Could not extract digest from push output"
    exit 1
fi

echo "Locally computed digest: sha256:$DIGEST"

# Sign the message
MESSAGE="${IMAGE_NAME}:${VERSION} sha256:${DIGEST}"

TMPFILE=$(mktemp)
echo -n "$MESSAGE" > "$TMPFILE"
openssl pkeyutl -sign -inkey "$KEY_FILE" -rawin -in "$TMPFILE" -out "${TMPFILE}.sig"
SIGNATURE=$(base64 -w0 < "${TMPFILE}.sig")
rm "$TMPFILE" "${TMPFILE}.sig"

# Get public key
PUBKEY=$(openssl pkey -in "$KEY_FILE" -pubout -outform DER | base64 -w0)

# Create signature container
cat > Containerfile.sig <<EOF
FROM scratch
LABEL energy.shem.digest="sha256:$DIGEST"
LABEL energy.shem.pubkey${KEY_NUMBER}="$PUBKEY"
LABEL energy.shem.signature${KEY_NUMBER}="$SIGNATURE"
EOF

podman build -f Containerfile.sig -t "$SIGNATURE_IMAGE" .
podman push "$SIGNATURE_IMAGE"
rm Containerfile.sig
```

A signing key can be created in this way:
```
openssl genpkey -algorithm ed25519 -out private.pem
```

## Checking for updates
The orchestrator keeps itself and all modules up to date in the following way:

1. For all installed modules and the orchestrator itself, it regularly checks the originating registries for new versions. The available versions are enumerated by listing the tags of the signature container, and in addition by pulling the "stable" tag of the signature container. All not-yet-known tags of the signature containers are pulled.
2. The orchestrator verifies the signatures using the locally stored public keys (each module has its own public key). If the signature for a certain digest is valid, it downloads the binary image using "podman pull image@digest". Otherwise, it logs an error message and does not download anything.
3. It schedules the updates with a random delay (0 to 96 hours). At the specified time, it sequentially tries updating all modules with a newer version (for the orchestrator, see below). For each module, it performs some checks to find out whether it works correctly. If a module fails to work, it marks this version as flawed and tries the next newest version.

The signature containers remain in the local repository. Even if the signature container on the registry is changed later, this may serve as an audit trail.


### Orchestrator Self-Update
At the scheduled time, the update of the orchestrator itself is handled as follows:

1. The running orchestrator extracts the orchestrator binary from the image and stores it in the $SHEM_HOME/bin directory with the version number attached (e.g., shem-orchestrator-v0.0.2).
2. It creates a marker file (e.g., shem-orchestrator-v0.0.2.try-me-once).
3. The orchestrator exits cleanly, triggering systemd to restart it.
4. On startup, it checks for try-me-once markers for versions newer than itself. If one exists, it removes the newest one and subsequently executes it with the flag "--verification-run". Standard output from the new orchestrator is piped to standard output.
5. The new orchestrator checks its own health. If everything works fine, it updates the symlink "shem-orchestrator" to point to its own binary. It then exits to be immediately restarted by systemd.

The failed binaries remain in the bin directory. They can be retried manually by creating a try-me-once marker file and asking systemd to restart the orchestrator. Old versions also remain to allow manual rollback. This works by just changing the symlink.

After step 2, the bin directory might look like this:

```
$SHEM_HOME/bin/
├── shem-orchestrator -> shem-orchestrator-v0.5.0  # Symlink used by systemd
├── shem-orchestrator-v0.5.0                       # Current version
├── shem-orchestrator-v0.6.0                       # Downloaded new version
└── shem-orchestrator-v0.6.0.try-me-once           # Version-specific marker
```

## Public Key Management
The public keys for each module and the orchestrator are stored in $SHEM_HOME/pubkeys/. For each module (and "shem-orchestrator") a file named after the module contains the public key.

A new module can be added with "shem-orchestrator add-module quay.io/publisher/module". This will prompt for the public key to be accepted in the future. If no public key is installed, the module will not be updated, as the signature verification step always fails.
