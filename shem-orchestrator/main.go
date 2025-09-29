package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// inject version number with ldflags="-X main.Version=0.0.0"
var Version = "undefined"

func main() {
	logger := NewLogger("orchestrator-main")

	// check compiled-in version number
	if _, _, _, err := parseVersion(Version); err != nil {
		logger.Error("Version '%s' is invalid (%v), please check build parameters.", Version, err)
		os.Exit(1)
	}

	// command line arguments
	var (
		verificationRun = flag.Bool("verification-run", false, "Used during self-update.")
		version         = flag.Bool("version", false, "Print version and exit.")
	)
	flag.Parse()

	if *version {
		fmt.Printf("shem-orchestrator version %s\n", Version)
		os.Exit(0)
	}

	// find and check home directory
	shemHome := os.Getenv("SHEM_HOME")
	if shemHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Error("failed to get user home directory: %v", err)
			os.Exit(1)
		}
		shemHome = filepath.Join(homeDir, "shem")
	}

	binDir := filepath.Join(shemHome, "bin")
	modulesDir := filepath.Join(shemHome, "modules")

	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		logger.Error("required directory does not exist: %s", binDir)
		os.Exit(1)
	}

	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		logger.Error("required directory does not exist: %s", modulesDir)
		os.Exit(1)
	}

	if !*verificationRun {
		// Initialize config manager to access orchestrator blacklist
		configManager := NewConfigManager(shemHome)
		orchestratorConfig, err := configManager.NewModuleConfig("orchestrator")
		if err != nil {
			logger.Error("failed to load orchestrator config: %v", err)
			os.Exit(1)
		}

		// Check for newer orchestrator versions that need verification
		newestVersion := findNewestOrchestratorVersion(logger, binDir, orchestratorConfig)
		if newestVersion != "" && compareVersions(newestVersion, Version) > 0 {
			logger.Info("found newer orchestrator binary with version %s", newestVersion)
			if err := orchestratorConfig.AddToBlacklist(newestVersion); err != nil {
				logger.Error("failed to add version %s to blacklist: %v", newestVersion, err)
			} else {
				logger.Info("added version %s to blacklist, executing verification run", newestVersion)
				binaryPath := filepath.Join(shemHome, "bin", "shem-orchestrator-"+newestVersion)
				executeVerificationRun(logger, binaryPath)
				// Note: executeVerificationRun does not return but calls os.Exit()
			}
		}
	}

	// Initialize orchestrator
	orchestrator, err := NewOrchestrator(shemHome, *verificationRun)
	if err != nil {
		logger.Error("failed to initialize orchestrator: %v", err)
		os.Exit(1)
	}

	// Run the orchestrator
	orchestrator.Run()
}

// findNewestOrchestratorVersion finds the newest non-blacklisted orchestrator version
func findNewestOrchestratorVersion(logger *Logger, binDir string, orchestratorConfig *ModuleConfig) string {
	// Get blacklisted versions
	blacklist, err := orchestratorConfig.GetBlacklistedVersions()
	if err != nil {
		logger.Error("failed to read orchestrator blacklist: %v", err)
		return ""
	}

	// Read bin directory for available binaries
	entries, err := os.ReadDir(binDir)
	if err != nil {
		logger.Error("failed to read bin directory: %v", err)
		return ""
	}

	newestVersion := ""

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Look for orchestrator binaries: shem-orchestrator-x.y.z
		if !strings.HasPrefix(name, "shem-orchestrator-") {
			continue
		}

		version := strings.TrimPrefix(name, "shem-orchestrator-")

		// Skip if not a valid version format
		if _, _, _, err := parseVersion(version); err != nil {
			continue
		}

		// Skip if version is blacklisted
		if _, isBlacklisted := blacklist[version]; isBlacklisted {
			logger.Debug("skipping blacklisted version %s", version)
			continue
		}

		// Compare with current newest candidate
		if newestVersion == "" || compareVersions(version, newestVersion) > 0 {
			newestVersion = version
		}
	}

	return newestVersion
}

// executeVerificationRun executes a newer orchestrator binary with verification run
func executeVerificationRun(logger *Logger, binaryPath string) {
	// Execute the binary with --verification-run flag
	logger.Info("executing verification run: %s --verification-run", binaryPath)
	cmd := exec.Command(binaryPath, "--verification-run")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		logger.Error("failed to start verification run: %v", err)
		os.Exit(1)
	} else {
		logger.Info("new orchestrator is being started")
	}

	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			logger.Error("verification run exited with code %d", exitCode)
			os.Exit(exitCode)
		} else {
			logger.Error("failed to execute verification run: %v", err)
			os.Exit(1)
		}
	}

	logger.Info("verification run executed successfully, exiting current process")
	os.Exit(0)
}
