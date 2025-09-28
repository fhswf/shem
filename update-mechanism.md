# Updates and Rollback
This document describes how the orchestrator handles updates for both modules and itself.

Updates are published via container registries. The orchestrator downloads the container images, verifies signatures, and updates modules and itself automatically. If a failure is detected, the offending update is rolled back.

## Initial installation
SHEM is installed by extracting an architecture-specific archive in the home directory of the user that SHEM will run as. Each supported architecture (amd64, arm64) has its own release archive containing the appropriate binaries and configuration. The archive contains the directory structure, the binary of the orchestrator, basic configuration files, and a systemd unit file.

```
$SHEM_HOME/                     # default: ~/shem
|-- bin/                        # orchestrator binaries
|-- modules/                    # information on installed modules
    |-- orchestrator            # the orchestrator is a special module
        |-- ...                 # module configuration files
    |-- mymodule1               # additional modules
        |-- ...
...
|-- shem-orchestrator.service
```

Container images will be stored wherever podman is configured to store them.

The orchestrator runs as a systemd service. It can be started manually, but it will exit and expect to be restarted during a self-update.

## Container registries
Modules, module updates, and orchestrator updates are published via container registries. Tags are used for different versions and include architecture suffixes for multi-architecture support. For each binary image, an accompanying image is published that contains the signature for the binary image. It is called amodule-sig:x.y.z-arch for the amodule:x.y.z-arch image.

For example, the orchestrator images might look like this:
```
quay.io/shem/
|── shem-orchestrator:0.0.1-amd64          # binary containers
|── shem-orchestrator:0.0.1-arm64
|── shem-orchestrator:0.0.2-amd64
|── shem-orchestrator:0.0.2-arm64
|── shem-orchestrator-sig:0.0.1-amd64      # signature containers (labels only)
|── shem-orchestrator-sig:0.0.1-arm64
|── shem-orchestrator-sig:0.0.2-amd64
|── shem-orchestrator-sig:0.0.2-arm64
|── shem-orchestrator-sig:latest-amd64      # always contains the most recent version
|── shem-orchestrator-sig:latest-arm64
```

## Signature Mechanism
SHEM uses Ed25519 signatures for verifying updates. OpenSSL's pkeyutl can be used to sign releases.

The signature covers the image name, tag (i.e., version and architecture), and the digest of the binary container. For example, the string "quay.io/shem/shem-orchestrator:0.0.1-amd64 sha256:3b4c5d6e..." is signed for the orchestrator binary, version 0.0.1 for amd64 architecture.

Both the public key and signature are stored as labels in a special signature container. In this example, the container would be called quay.io/shem/shem-orchestrator-sig:0.0.1-amd64.

The Containerfile might look like this:
```dockerfile
FROM scratch
LABEL org.opencontainers.image.version="0.0.1-amd64"
LABEL energy.shem.digest="sha256:3b4c5d6e..."
LABEL energy.shem.pubkey="cQyjQftwIlSGYvWjfDMzpr0B5/Lr/S8jDFfVW3hOBk0="
LABEL energy.shem.signature="AiMEIX/R..."
```

### Creating Signature Containers
When signing containers, we have to make sure to compute the digest locally (otherwise we would trust the registry to not change the container). According to the [OCI spec](https://github.com/opencontainers/distribution-spec/blob/main/spec.md#push), the digest of every upload is computed by the client and then re-computed and returned by the registry. In the following example, we use podman push with the --digestfile parameter to get the digest. We cannot simply use the digest of the container in local storage, as it will often be different from the uploaded version due to different compression settings.

```bash
#!/bin/bash
set -e

REGISTRY="$1"     # e.g., quay.io/shem
IMAGE_NAME="$2"   # e.g., shem-orchestrator
VERSION="$3"      # e.g., 0.0.1
ARCH="$4"         # e.g., amd64
KEY_FILE="$5"     # e.g., signing-key.pem

LOCAL_IMAGE="localhost/${IMAGE_NAME}:${VERSION}-${ARCH}"
REGISTRY_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}-${ARCH}"
SIGNATURE_IMAGE="${REGISTRY}/${IMAGE_NAME}-sig:${VERSION}-${ARCH}"
SIGNATURE_LATEST="${REGISTRY}/${IMAGE_NAME}-sig:latest-${ARCH}"

# Push and capture locally computed digest
DIGEST_FILE=$(mktemp)
echo "Pushing to registry..."
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
```

A signing key can be created in this way:
```
openssl genpkey -algorithm ed25519 -out signing-key.pem
```

## Automatic Module Updates
Modules have their configuration stored in individual directories under `$SHEM_HOME/modules/[module_name]`. For automatic updates to be enabled, a module directory must contain both an `image` file (specifying the container image) and a `public_key` file (containing the base64-encoded public key of the publisher).

For example, the orchestrator module configuration for automatic updates would be stored as:
```
$SHEM_HOME/modules/orchestrator/
|-- image  [quay.io/shem/shem-orchestrator]
|-- public_key  [cQyjQftwIlSGYvWjfDMzpr0B5/Lr/S8jDFfVW3hOBk0=]
```

The orchestrator will regularly check for updates for all modules that have `public_key` files in their configuration directories. Modules without a `public_key` file that are available in local storage (e.g., because you pulled them manually) can still be used but won't be automatically updated.

A new module can also be added using the `add-module` command:

```bash
shem-orchestrator add-module mymodule quay.io/publisher/mymodule
```

This command will:
1. Pull a corresponding signature container (e.g., `quay.io/publisher/mymodule-sig:latest-amd64`)
2. Extract the public key and ask the user if it should be added.
3. If yes, create the module directory `$SHEM_HOME/modules/mymodule/` and write the image name to the `image` file and the public key to the `public_key` file, then trigger the update process, which will verify and pull the latest version of the module.

## Module Blacklist
The orchestrator maintains per-module blacklists in `$SHEM_HOME/modules/[module_name]/blacklist` files that contain versions that failed to work previously and are skipped when searching for updates. Each blacklisted version is listed on a separate line.

## Checking for updates
The orchestrator keeps itself and the modules up to date. For each module that has a `public_key` file in its configuration directory, it proceeds in the following way:

1. It regularly checks the originating registries for new versions. The available versions are enumerated by listing the tags of the signature container, and in addition by pulling the "latest-[arch]" tag of the signature container (as listing all tags might fail).

2. The orchestrator selects the version of the signature container to pull by taking the latest version (highest version number) that is:
   - Not on the module's blacklist (stored in `$SHEM_HOME/modules/[module_name]/blacklist`)
   - Higher than the highest version already pulled and not on the blacklist.

   If no such version exists, the updater skips this module.

3. The orchestrator verifies the signatures using the public key stored in the module's `public_key` file. If the signature is valid, it downloads the binary image using "podman pull image@digest". If signature verification fails, it returns to step 2 while ignoring this version.

4. It schedules the updates with a random delay (0 to 96 hours). At the specified time, it stops the old module and starts the new one (for orchestrator updates, see below). If the new version fails to work correctly, it adds this version to the module's blacklist file (`$SHEM_HOME/modules/[module_name]/blacklist`). The updater will then, on its next run, skip this version and try the next older one.

The signature containers remain in the local repository. Even if the signature container on the registry is changed later, this may serve as an audit trail.

### Orchestrator Self-Update
For everyting except for the update itself the orchestrator is just treated as any other module. However, the update has to be performed differently. At the scheduled time, the orchestrator updates itself as follows:

1. The running orchestrator extracts the new orchestrator binary from the image and stores it in the $SHEM_HOME/bin directory with the version number attached (e.g., shem-orchestrator-0.0.2).
2. It exits cleanly, triggering systemd to restart it.
3. On startup, it checks for versions newer than itself that are not on the orchestrator's blacklist (stored in `$SHEM_HOME/modules/orchestrator/blacklist`). If one exists, it puts it on the blacklist first and then executes it with the flag "--verification-run". Standard output/error from the new orchestrator is piped to standard output/error.
4. The new orchestrator starts up and checks its own health after a few minutes. If everything works fine, it updates the symlink "shem-orchestrator" to point to its own binary and removes itself from the blacklist. It then exits to be immediately restarted by systemd.
