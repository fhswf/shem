package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

/*
naming convention:
	imageAndTag: quay.io/shem/shem-orchestrator:0.0.1-amd64
	image or baseImage: quay.io/shem/shem-orchestrator
	corresponding signature image: quay.io/shem/shem-orchestrator-sig
	tag: 0.0.1-amd64
	version: 0.0.1
	architecture: amd64
*/

type UpdateManager struct {
	configManager      *ConfigManager
	orchestratorConfig *ModuleConfig
	shemHome           string
	verificationRun    bool
	logger             *Logger
	updateChannel      chan string
	cancelFunc         context.CancelFunc
	scheduledUpdates   map[string]string // maps module name to scheduled version
}

// NewUpdateManager creates a new update manager instance
func NewUpdateManager(configManager *ConfigManager, verificationRun bool) *UpdateManager {
	logger := NewLogger("orchestrator-updatemanager")

	orchestratorConfig, err := configManager.NewModuleConfig("orchestrator")
	if err != nil {
		logger.Error("failed to load orchestrator config: %v", err)
		// Continue with nil config - methods will handle errors
	}

	return &UpdateManager{
		configManager:      configManager,
		orchestratorConfig: orchestratorConfig,
		shemHome:           configManager.shemHome,
		verificationRun:    verificationRun,
		logger:             logger,
		updateChannel:      make(chan string, 100),
		scheduledUpdates:   make(map[string]string),
	}
}

// Run runs the update manager until the context is canceled
func (um *UpdateManager) Run(ctx context.Context, cancel context.CancelFunc) {
	um.logger.Info("starting update manager")

	// Store the cancel function for orchestrator restart
	um.cancelFunc = cancel

	// Create a ticker for regular update checks using config interval
	checkIntervalHours, err := um.orchestratorConfig.GetFloat("UpdateCheckIntervalHours", 22.15)
	if err != nil {
		um.logger.Error("failed to get UpdateCheckIntervalHours: %v", err)
		return
	}
	checkInterval := time.Duration(checkIntervalHours * float64(time.Hour))
	updateTicker := time.NewTicker(checkInterval)
	defer updateTicker.Stop()

	// Main loop
	for {
		select {
		case <-ctx.Done():
			um.logger.Info("stopping update manager")
			return
		case <-updateTicker.C:
			if err := um.checkAndScheduleUpdates(); err != nil {
				um.logger.Error("error checking for updates: %v", err)
			}
		case image := <-um.updateChannel:
			um.logger.Info("executing scheduled update for module: %s", image)
			if err := um.updateModule(image); err != nil {
				um.logger.Error("error updating module %s: %v", image, err)
			}
		}
	}
}

// parseVersion parses a version string in x.y.z format and returns major, minor, patch
func parseVersion(version string) (int, int, int, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return major, minor, patch, nil
}

// compareVersions compares two version strings in x.y.z format; an invalid string is treated as 0.0.0
// Returns: -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
func compareVersions(v1, v2 string) int {
	// errors are ignored; if an error occurs, the version is 0.0.0, which is always older
	maj1, min1, pat1, _ := parseVersion(v1)
	maj2, min2, pat2, _ := parseVersion(v2)

	if maj1 != maj2 {
		if maj1 > maj2 {
			return 1
		}
		return -1
	}

	if min1 != min2 {
		if min1 > min2 {
			return 1
		}
		return -1
	}

	if pat1 != pat2 {
		if pat1 > pat2 {
			return 1
		}
		return -1
	}

	return 0
}

// findLocalVersions uses podman to find all binary containers with correct architecture in local storage
// Returns a set of versions
func (um *UpdateManager) findLocalVersions(image string) (map[string]struct{}, error) {
	// Execute podman images command to list only images for the specific module
	cmd := exec.Command("podman", "images", "--filter", "reference="+image, "--format", "{{.Tag}}")
	output, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to execute podman images: %w, %s", err, ee.Stderr)
		} else {
			return nil, fmt.Errorf("failed to execute podman images: %w", err)
		}
	}

	versions := make(map[string]struct{})

	// Parse output line by line
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		tag := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if tag == "" {
			continue
		}

		version, arch, _ := um.extractVersionAndArch(tag)
		if arch == runtime.GOARCH {
			versions[version] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error parsing podman output: %w", err)
	}

	um.logger.Debug("found %d local versions for module %s", len(versions), image)
	return versions, nil
}

// findRemoteVersions searches for remote signature containers and pulls latest tags to discover versions
func (um *UpdateManager) findRemoteVersions(image string) (map[string]struct{}, error) {
	remoteVersions := make(map[string]struct{})

	// Search for remote signature containers for this base image
	tags, err := um.listRemoteSignatureTags(image)
	if err != nil {
		return nil, fmt.Errorf("failed to search remote signature tags for %s: %v", image, err)
	}

	for _, tag := range tags {
		version, arch, err := um.extractVersionAndArch(tag)
		if err == nil && arch == runtime.GOARCH {
			remoteVersions[version] = struct{}{}
		}
	}

	// Pull latest tag to discover its version
	latestImageAndTag := image + "-sig:latest-" + runtime.GOARCH
	latestVersion, err := um.extractVersionLabel(latestImageAndTag)
	if err != nil {
		um.logger.Warn("failed to pull latest version for %s: %v", image, err)
	} else if latestVersion != "" {
		_, _, _, err := parseVersion(latestVersion)
		if err == nil {
			// Add latest version to the set (version only, no architecture suffix)
			remoteVersions[latestVersion] = struct{}{}
		}
	}

	um.logger.Info("found %d remote versions for module image %s", len(remoteVersions), image)
	return remoteVersions, nil
}

// listRemoteSignatureTags uses podman search --list-tags to find all signature container tags
func (um *UpdateManager) listRemoteSignatureTags(baseImage string) ([]string, error) {
	// Search for signature containers: baseImage + "-sig"
	sigImage := baseImage + "-sig"

	cmd := exec.Command("podman", "search", sigImage, "--list-tags", "--limit", "10000", "--format", "{{.Tag}}")
	output, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to search tags for %s: %w, %s", sigImage, err, ee.Stderr)
		} else {
			return nil, fmt.Errorf("failed to search tags for %s: %w", sigImage, err)
		}
	}

	var tags []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		tag := strings.TrimSpace(scanner.Text())
		if tag != "" {
			tags = append(tags, tag)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error parsing search output: %w", err)
	}

	um.logger.Debug("found %d signature container tags for image %s", len(tags), baseImage)
	return tags, nil
}

// extractVersionLabel pulls an image (usually the "latest-[arch]" version of a signature container)
// and extracts its version from labels
// Returns just the version string (without architecture suffix)
func (um *UpdateManager) extractVersionLabel(imageAndTag string) (string, error) {
	// Pull the image
	cmd := exec.Command("podman", "pull", imageAndTag)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to pull %s: %w", imageAndTag, err)
	}

	um.logger.Debug("pulled image: %s", imageAndTag)

	// Get standard OCI version annotation
	cmd = exec.Command("podman", "inspect", "--format", "{{index .Config.Labels \"org.opencontainers.image.version\"}}", imageAndTag)
	output, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("failed to inspect %s: %w, %s", imageAndTag, err, ee.Stderr)
		} else {
			return "", fmt.Errorf("failed to inspect %s: %w", imageAndTag, err)
		}
	}

	version := strings.TrimSpace(string(output))
	if version != "" && version != "<no value>" {
		return version, nil
	}

	return "", fmt.Errorf("no version label found in %s", imageAndTag)
}

// SignatureData holds the extracted signature information from a signature container
type SignatureData struct {
	Digest    string
	PublicKey string
	Signature string
}

// verifyAndPullImage pulls a signature container, verifies its signature, and pulls the binary container
func (um *UpdateManager) verifyAndPullImage(baseImage, tag, modulePublicKey string) error {
	sigImage := baseImage + "-sig:" + tag

	// Pull the signature container
	um.logger.Debug("pulling signature container: %s", sigImage)
	cmd := exec.Command("podman", "pull", sigImage)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull signature container %s: %w", sigImage, err)
	}

	// Extract signature data from the container
	sigData, err := um.extractSignatureData(sigImage)
	if err != nil {
		return fmt.Errorf("failed to extract signature data from %s: %w", sigImage, err)
	}

	// Verify the signature
	if err := um.verifySignature(baseImage, tag, sigData, modulePublicKey); err != nil {
		return fmt.Errorf("signature verification failed for %s:%s: %w", baseImage, tag, err)
	}

	um.logger.Info("signature verified for %s:%s", baseImage, tag)

	// Pull the binary container by digest
	binaryImage := baseImage + "@" + sigData.Digest
	um.logger.Debug("pulling binary container: %s", binaryImage)
	cmd = exec.Command("podman", "pull", binaryImage)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull binary container %s: %w", binaryImage, err)
	}

	// Tag the digest-pulled image with version tag (findLocalVersions searches for tags)
	versionTag := baseImage + ":" + tag
	um.logger.Debug("tagging image %s as %s", binaryImage, versionTag)
	cmd = exec.Command("podman", "tag", binaryImage, versionTag)
	if err := cmd.Run(); err != nil {
		um.logger.Warn("failed to tag image %s as %s: %v", binaryImage, versionTag, err)
	}

	um.logger.Info("successfully verified and pulled %s:%s", baseImage, tag)
	return nil
}

// extractSignatureData extracts digest, public key, and signature from signature container labels
func (um *UpdateManager) extractSignatureData(sigImage string) (*SignatureData, error) {
	// Extract digest
	digestCmd := exec.Command("podman", "inspect", "--format", "{{index .Config.Labels \"energy.shem.digest\"}}", sigImage)
	digestOutput, err := digestCmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to extract digest: %w, %s", err, ee.Stderr)
		} else {
			return nil, fmt.Errorf("failed to extract digest: %w", err)
		}
	}
	digest := strings.TrimSpace(string(digestOutput))
	if digest == "" || digest == "<no value>" {
		return nil, fmt.Errorf("digest not found in signature container")
	}

	// Extract public key
	pubkeyCmd := exec.Command("podman", "inspect", "--format", "{{index .Config.Labels \"energy.shem.pubkey\"}}", sigImage)
	pubkeyOutput, err := pubkeyCmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to extract public key: %w, %s", err, ee.Stderr)
		} else {
			return nil, fmt.Errorf("failed to extract public key: %w", err)
		}
	}
	pubkey := strings.TrimSpace(string(pubkeyOutput))
	if pubkey == "" || pubkey == "<no value>" {
		return nil, fmt.Errorf("public key not found in signature container")
	}

	// Extract signature
	sigCmd := exec.Command("podman", "inspect", "--format", "{{index .Config.Labels \"energy.shem.signature\"}}", sigImage)
	sigOutput, err := sigCmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to extract signature: %w, %s", err, ee.Stderr)
		} else {
			return nil, fmt.Errorf("failed to extract signature: %w", err)
		}
	}
	signature := strings.TrimSpace(string(sigOutput))
	if signature == "" || signature == "<no value>" {
		return nil, fmt.Errorf("signature not found in signature container")
	}

	return &SignatureData{
		Digest:    digest,
		PublicKey: pubkey,
		Signature: signature,
	}, nil
}

// verifySignature verifies the Ed25519 signature against the expected message
func (um *UpdateManager) verifySignature(baseImage, tag string, sigData *SignatureData, modulePublicKey string) error {
	// Check if the public key in the signature matches the module's public key
	if sigData.PublicKey != modulePublicKey {
		return fmt.Errorf("public key mismatch: container has %s, module expects %s",
			sigData.PublicKey, modulePublicKey)
	}

	// Decode the base64 public key
	pubKeyBytes, err := base64.StdEncoding.DecodeString(modulePublicKey)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	// Ensure public key is the right length for Ed25519
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: expected %d, got %d",
			ed25519.PublicKeySize, len(pubKeyBytes))
	}

	// Decode the base64 signature
	signatureBytes, err := base64.StdEncoding.DecodeString(sigData.Signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Construct the message that was signed: "baseImage:version digest"
	message := baseImage + ":" + tag + " " + sigData.Digest

	// Verify the signature
	publicKey := ed25519.PublicKey(pubKeyBytes)
	if !ed25519.Verify(publicKey, []byte(message), signatureBytes) {
		return fmt.Errorf("signature verification failed for message: %s", message)
	}

	um.logger.Debug("signature verified for message: %s", message)
	return nil
}

// findLatestEligibleVersion finds the latest eligible version of a module
// according to the update mechanism specification. It enumerates available versions
// using findRemoteVersions, then selects the highest version that is not blacklisted
// and higher than the specified minimum version.
func (um *UpdateManager) findLatestEligibleVersion(image string, minimumVersion string, blacklist map[string]struct{}) (string, error) {
	// Get available versions using findRemoteVersions
	versionsMap, err := um.findRemoteVersions(image)
	if err != nil {
		return "", fmt.Errorf("failed to find remote versions for image %s: %w", image, err)
	}

	if len(versionsMap) == 0 {
		return "", fmt.Errorf("no versions found for image %s", image)
	}

	// Find the latest eligible version
	var latestVersion string
	for version := range versionsMap {
		// Skip if version is blacklisted
		if _, isBlacklisted := blacklist[version]; isBlacklisted {
			um.logger.Debug("skipping blacklisted version %s for image %s", version, image)
			continue
		}

		// Skip if version is not higher than minimum version
		if minimumVersion != "" && compareVersions(version, minimumVersion) <= 0 {
			um.logger.Debug("skipping version %s for image %s (not higher than minimum %s)", version, image, minimumVersion)
			continue
		}

		// Compare with current latest candidate
		if latestVersion == "" {
			latestVersion = version
		} else {
			if compareVersions(version, latestVersion) > 0 {
				latestVersion = version
			}
		}
	}

	if latestVersion == "" {
		return "", fmt.Errorf("no eligible version found for image %s (minimum: %s)", image, minimumVersion)
	}

	um.logger.Info("found latest eligible version %s for image %s (minimum: %s)", latestVersion, image, minimumVersion)
	return latestVersion, nil
}

// extractVersionAndArch extracts both version and architecture from a tag
// Assumes version format is x.y.z-arch, returns version and architecture separately
// For example: "1.2.3-amd64" -> ("1.2.3", "amd64")
func (um *UpdateManager) extractVersionAndArch(tag string) (string, string, error) {
	dashIndex := strings.Index(tag, "-")
	if dashIndex == -1 {
		return "", "", fmt.Errorf("no dash in tag '%s'", tag)
	}
	version := tag[:dashIndex]
	arch := tag[dashIndex+1:]
	_, _, _, err := parseVersion(version)

	return version, arch, err
}

// currentModuleVersion returns the current version of a module
// Returns the orchestrator version for the shem-orchestrator module, empty string for all others
func (um *UpdateManager) currentModuleVersion(moduleName string) string {
	// Check if this is the orchestrator module
	if moduleName == "orchestrator" {
		return Version
	}

	// For all other modules, read current_version from config
	moduleConfig, err := um.configManager.NewModuleConfig(moduleName)
	if err != nil {
		um.logger.Error("failed to load config for module %s: %v", moduleName, err)
		return ""
	}

	currentVersion, err := moduleConfig.GetString("current_version")
	if err != nil {
		um.logger.Debug("no current version found for module %s", moduleName)
		return ""
	}

	return currentVersion
}

// checkAndScheduleUpdates checks for updates for all modules and schedules them
func (um *UpdateManager) checkAndScheduleUpdates() error {
	// Load modules configuration
	moduleNames, err := um.configManager.ListModules()
	if err != nil {
		return fmt.Errorf("failed to list modules: %w", err)
	}

	um.logger.Info("checking for updates for %d modules", len(moduleNames))

	// Iterate through all modules
	for _, moduleName := range moduleNames {
		moduleConfig, err := um.configManager.NewModuleConfig(moduleName)
		if err != nil {
			um.logger.Error("failed to load config for module %s: %v", moduleName, err)
			continue
		}

		// Get image name
		image, err := moduleConfig.GetString("image")
		if err != nil {
			um.logger.Error("failed to get image for module %s: %v", moduleName, err)
			continue
		}

		// Skip modules without public key (no auto-updates)
		publicKey, err := moduleConfig.GetString("public_key")
		if err != nil {
			um.logger.Debug("no public key found for module %s, skipping auto-updates", moduleName)
			continue
		}

		um.logger.Debug("checking for updates for module: %s (image: %s)", moduleName, image)

		// Get current version of the module
		currentVersion := um.currentModuleVersion(moduleName)

		// Determine minimum version (use scheduled version if exists, otherwise current)
		minimumVersion := currentVersion
		if scheduledVersion, exists := um.scheduledUpdates[moduleName]; exists {
			minimumVersion = scheduledVersion
		}

		// Get module-specific blacklist
		blacklist, err := moduleConfig.GetBlacklistedVersions()
		if err != nil {
			um.logger.Error("failed to read blacklist for module %s: %v", moduleName, err)
			continue
		}

		// Keep trying to find updates until we succeed or run out of versions
		for {
			// Find the latest eligible version
			latestVersion, err := um.findLatestEligibleVersion(image, minimumVersion, blacklist)
			if err != nil {
				um.logger.Debug("no eligible update found for module %s: %v", image, err)
				break // No more updates available
			}

			um.logger.Info("found potential update for module %s: %s -> %s", image, currentVersion, latestVersion)

			// Try to verify and pull the binary
			err = um.verifyAndPullImage(image, latestVersion+"-"+runtime.GOARCH, publicKey)
			if err != nil {
				um.logger.Warn("verification failed for module %s version %s: %v", image, latestVersion, err)

				// Add this version to module's blacklist and try again
				blacklist[latestVersion] = struct{}{}
				continue
			}

			// Verification successful
			um.logger.Info("signature verification successful for module %s version %s", image, latestVersion)

			// Check if we should schedule the update (skip shem-orchestrator during verification run)
			if um.verificationRun && moduleName == "orchestrator" {
				um.logger.Info("skipping shem-orchestrator update scheduling during verification run")
			} else {
				// Schedule the update
				um.logger.Info("scheduling update for module %s to version %s", moduleName, latestVersion)
				um.scheduleUpdate(moduleName, latestVersion)
			}
			break // Successfully found and processed an update
		}
	}

	return nil
}

// scheduleUpdate schedules a module update with a random delay up to UpdateDelayMaxHours
func (um *UpdateManager) scheduleUpdate(moduleName, newVersion string) {
	// Generate random delay between 0 and UpdateDelayMaxHours
	maxDelayHours, err := um.orchestratorConfig.GetFloat("UpdateDelayMaxHours", 96.0)
	if err != nil {
		um.logger.Error("failed to get UpdateDelayMaxHours: %v", err)
		return
	}
	delayHours := rand.Float64() * maxDelayHours
	delay := time.Duration(delayHours * float64(time.Hour))

	// Record the scheduled update
	um.scheduledUpdates[moduleName] = newVersion

	um.logger.Info("update scheduled: %s -> %s (will execute in %.1f hours)",
		moduleName, newVersion, delayHours)

	// Start a goroutine to send the update message after the delay
	go func() {
		time.Sleep(delay)
		select {
		case um.updateChannel <- moduleName:
			// Update message sent successfully
		default:
			// Channel is full, log warning
			um.logger.Warn("update channel full, dropping scheduled update for %s", moduleName)
		}
	}()
}

// updateModule updates the module to the newest installed version
func (um *UpdateManager) updateModule(moduleName string) error {
	// Clean up scheduled update entry
	delete(um.scheduledUpdates, moduleName)

	// Get image name from module config
	moduleConfig, err := um.configManager.NewModuleConfig(moduleName)
	if err != nil {
		return fmt.Errorf("failed to load config for module %s: %w", moduleName, err)
	}

	image, err := moduleConfig.GetString("image")
	if err != nil {
		return fmt.Errorf("failed to get image for module %s: %w", moduleName, err)
	}

	// Use findLocalVersions to find all local versions
	localVersions, err := um.findLocalVersions(image)
	if err != nil {
		return fmt.Errorf("failed to find local versions for %s: %w", image, err)
	}

	if len(localVersions) == 0 {
		return fmt.Errorf("no local versions found for image %s", image)
	}

	// Find the newest version using compareVersions
	var newestVersion string
	for version := range localVersions {
		if newestVersion == "" {
			newestVersion = version
		} else if compareVersions(version, newestVersion) > 0 {
			newestVersion = version
		}
	}

	// Check whether it is newer than the currentModuleVersion(); if not, exit
	currentVersion := um.currentModuleVersion(moduleName)
	if currentVersion != "" && compareVersions(newestVersion, currentVersion) <= 0 {
		um.logger.Info("newest local version %s is not newer than current version %s for module %s", newestVersion, currentVersion, moduleName)
		return nil
	}

	// Check whether module is different from "shem-orchestrator"; if so, skip as not implemented for now
	if !(moduleName == "orchestrator") {
		um.logger.Info("module update not yet implemented for non-orchestrator modules: %s", moduleName)
		return nil
	}

	// Extract the orchestrator binary from the image directly to target location
	targetPath := filepath.Join(um.shemHome, "bin", "shem-orchestrator-"+newestVersion)
	err = um.extractBinaryFromImage(image, newestVersion+"-"+runtime.GOARCH, targetPath)
	if err != nil {
		return fmt.Errorf("failed to extract binary from image %s:%s: %w", image, newestVersion, err)
	}

	um.logger.Info("successfully extracted orchestrator binary for version %s", newestVersion)

	// Trigger restart of orchestrator
	return um.triggerOrchestratorRestart(newestVersion)
}

// extractBinaryFromImage extracts the /shem-orchestrator binary from a container image to targetPath
func (um *UpdateManager) extractBinaryFromImage(image, tag, targetPath string) error {
	// Create a temporary container from the image
	imageAndTag := image + ":" + tag
	containerName := "shem-orchestrator-extract-" + tag

	// Create container without starting it
	cmd := exec.Command("podman", "create", "--replace", "--name", containerName, imageAndTag, "/bin/true")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("failed to create container from image %s: %w, %s", imageAndTag, err, ee.Stderr)
		} else {
			return fmt.Errorf("failed to create container from image %s: %w", imageAndTag, err)
		}
	}

	// Ensure container is removed on exit
	defer func() {
		exec.Command("podman", "rm", containerName).Run()
	}()

	// Copy the binary directly to the target path
	cmd = exec.Command("podman", "cp", containerName+":/shem-orchestrator", targetPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy binary from container: %w", err)
	}

	um.logger.Debug("extracted binary from %s to %s", imageAndTag, targetPath)
	return nil
}

// triggerOrchestratorRestart triggers a restart of the orchestrator with the new version
func (um *UpdateManager) triggerOrchestratorRestart(newVersion string) error {
	um.logger.Info("restart triggered for orchestrator version %s", newVersion)

	// Trigger graceful shutdown of the orchestrator
	// The orchestrator will detect the shutdown and restart with the new version
	if um.cancelFunc != nil {
		um.logger.Info("initiating graceful orchestrator shutdown for restart")
		um.cancelFunc()
	} else {
		return fmt.Errorf("cannot restart orchestrator: cancel function not available")
	}

	return nil
}
