package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fhswf/shem/shemmsg"
)

// ModuleManager manages the lifecycle of SHEM modules
type ModuleManager struct {
	configManager *ConfigManager
	logger        *Logger
	modules       map[string]*ModuleInstance // only contains running modules
	mu            sync.Mutex
}

// ModuleInstance represents a running module
type ModuleInstance struct {
	name          string
	image         string // base image name without version/arch tag
	version       string
	containerName string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderr        io.ReadCloser
	logger        *Logger
}

// NewModuleManager creates a new module manager
func NewModuleManager(configManager *ConfigManager) *ModuleManager {
	return &ModuleManager{
		configManager: configManager,
		logger:        NewLogger("module-manager"),
		modules:       make(map[string]*ModuleInstance),
	}
}

// Run runs the module manager reconciliation loop until ctx is canceled
func (mm *ModuleManager) Run(ctx context.Context) {
	mm.logger.Info("starting module manager")

	// Run reconciliation immediately, then every 10 seconds
	mm.reconcile()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mm.reconcile()
		case <-ctx.Done():
			mm.stopAllModules()
			mm.logger.Info("module manager stopped")
			return
		}
	}
}

// reconcile compares desired module state (config on disk) with actual state and acts
func (mm *ModuleManager) reconcile() {
	// First step: remove orphaned containers (containers might be asked to stop in the second and
	// third step; if they have not stopped running when this function is called again ten
	// seconds later, they will be removed here)
	mm.cleanupOrphanedContainers()

	// Second step: reconcile desired state
	moduleNames, err := mm.configManager.ListModules()
	if err != nil {
		mm.logger.Error("failed to list modules: %v", err)
		return
	}

	for _, name := range moduleNames {
		if name == "orchestrator" {
			continue
		}

		mm.mu.Lock()
		instance := mm.modules[name]
		mm.mu.Unlock()

		moduleDir := filepath.Join(mm.configManager.shemHome, "modules", name)

		// Handle disabled file
		disabledPath := filepath.Join(moduleDir, "disabled")
		if _, err := os.Stat(disabledPath); err == nil {
			if instance != nil {
				mm.logger.Info("module %s is disabled, stopping", name)
				mm.requestStop(instance)
			}
			continue
		}

		// Handle restart file
		restartPath := filepath.Join(moduleDir, "restart")
		if _, err := os.Stat(restartPath); err == nil {
			os.Remove(restartPath)
			if instance != nil {
				mm.logger.Info("restart requested for module %s", name)
				mm.requestStop(instance)
				continue
			} else {
				mm.logger.Info("restart requested for module %s, but it is not running", name)
			}
		}

		// If module is running, check if config changed
		if instance != nil {
			moduleConfig, err := mm.configManager.NewModuleConfig(name)
			if err != nil {
				mm.logger.Error("failed to get config for module %s: %v", name, err)
				continue
			}

			version, err := moduleConfig.GetString("current_version", "")
			if err != nil {
				mm.logger.Error("failed to get current_version for %s: %v", name, err)
				continue
			}

			image, err := moduleConfig.GetString("image", "")
			if err != nil {
				mm.logger.Error("failed to get image for %s: %v", name, err)
				continue
			}

			if instance.image == image && instance.version == version {
				continue // up to date, nothing to do
			}

			mm.logger.Info("config changed for module %s, restarting", name)
			mm.requestStop(instance)
			continue
		}

		// No running instance, try to start
		moduleConfig, err := mm.configManager.NewModuleConfig(name)
		if err != nil {
			mm.logger.Error("failed to get config for module %s: %v", name, err)
			continue
		}

		version, err := moduleConfig.GetString("current_version", "")
		if err != nil {
			mm.logger.Error("failed to get current_version for %s: %v", name, err)
			continue
		}
		if version == "" {
			continue
		}

		image, err := moduleConfig.GetString("image", "")
		if err != nil {
			mm.logger.Error("failed to get image for %s: %v", name, err)
			continue
		}
		if image == "" {
			mm.logger.Warn("module %s has no image set", name)
			continue
		}

		if err := mm.startModule(name, image, version); err != nil {
			mm.logger.Error("failed to start module %s: %v", name, err)
		}
	}

	// Third step: stop modules no longer in config
	desired := make(map[string]struct{}, len(moduleNames))
	for _, name := range moduleNames {
		desired[name] = struct{}{}
	}

	mm.mu.Lock()
	var toStop []*ModuleInstance
	for name, instance := range mm.modules {
		if _, ok := desired[name]; !ok {
			toStop = append(toStop, instance)
		}
	}
	mm.mu.Unlock()

	for _, instance := range toStop {
		mm.logger.Info("module %s removed from config, stopping", instance.name)
		mm.requestStop(instance)
	}
}

// cleanupOrphanedContainers finds and removes any shem-module-* containers
// that are not tracked by the module manager
func (mm *ModuleManager) cleanupOrphanedContainers() {
	out, err := exec.Command("podman", "ps", "-a",
		"--filter", "name=shem-module-",
		"--format", "{{.Names}}").Output()
	if err != nil {
		mm.logger.Error("failed to list containers: %v", err)
		return
	}

	// Build set of expected container names
	mm.mu.Lock()
	expected := make(map[string]struct{})
	for _, instance := range mm.modules {
		expected[instance.containerName] = struct{}{}
	}
	mm.mu.Unlock()

	// Remove orphaned containers
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		if _, ok := expected[name]; !ok {
			mm.logger.Warn("removing orphaned container: %s", name)
			if err := exec.Command("podman", "rm", "-fi", name).Run(); err != nil {
				mm.logger.Error("failed to remove container %s: %v", name, err)
			}
		}
	}
}

// requestStop initiates a graceful stop by closing stdin and removes the
// instance from the map. The container becomes an orphan and will be cleaned
// up by cleanupOrphanedContainers on the next reconcile tick if it hasn't
// exited by then.
func (mm *ModuleManager) requestStop(instance *ModuleInstance) {
	instance.logger.Info("closing stdin to request shutdown")
	instance.stdin.Close()

	mm.mu.Lock()
	delete(mm.modules, instance.name)
	mm.mu.Unlock()
}

// startModule starts a single module with the given image and version
func (mm *ModuleManager) startModule(moduleName, image, version string) error {
	containerName := fmt.Sprintf("shem-module-%s", moduleName)
	fullImage := fmt.Sprintf("%s:%s-%s", image, version, runtime.GOARCH)

	mm.logger.Info("starting module %s (image: %s)", moduleName, fullImage)

	cmd := mm.buildPodmanCommand(moduleName, containerName, fullImage)

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	instance := &ModuleInstance{
		name:          moduleName,
		image:         image,
		version:       version,
		containerName: containerName,
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		logger:        NewLogger(fmt.Sprintf("module-%s", moduleName)),
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	instance.logger.Info("started container %s", containerName)

	mm.mu.Lock()
	mm.modules[moduleName] = instance
	mm.mu.Unlock()

	go mm.watchModule(instance)

	return nil
}

// watchModule reads stdout/stderr and waits for the process to exit
func (mm *ModuleManager) watchModule(instance *ModuleInstance) {
	defer func() {
		mm.mu.Lock()
		delete(mm.modules, instance.name)
		mm.mu.Unlock()
	}()

	// Read and parse stdout messages
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		if instance.stdout == nil {
			return
		}
		reader := shemmsg.NewReader(instance.stdout)
		for {
			msg, err := reader.Read()
			if err == io.EOF {
				return
			}
			if err != nil {
				instance.logger.Warn("invalid message: %v", err)
				continue
			}

			// Validate that the name is unqualified (no dots)
			if err := shemmsg.ValidateNamePart(msg.Name); err != nil {
				instance.logger.Warn("invalid variable name %q: %v", msg.Name, err)
				continue
			}

			// Qualify the variable name with the module name
			msg = msg.WithName(instance.name + "." + msg.Name)

			instance.logger.Info("received %s %s", msg.Type(), msg.Name)

			// TODO: route message to subscribing modules
		}
	}()

	// Read stderr and pass it on as log entries
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		if instance.stderr == nil {
			return
		}
		scanner := bufio.NewScanner(instance.stderr)
		for scanner.Scan() {
			instance.logger.Log("%s", scanner.Text())
		}
	}()

	// Wait for the process to exit
	err := instance.cmd.Wait()

	// Wait for stdout and stderr to be fully read
	<-stdoutDone
	<-stderrDone

	if err != nil {
		instance.logger.Error("module exited with error: %v", err)
	} else {
		instance.logger.Info("module exited")
	}
}

// stopAllModules stops all module containers and if necessary kills them
func (mm *ModuleManager) stopAllModules() {
	mm.logger.Info("stopping all modules")

	mm.mu.Lock()
	instances := slices.Collect(maps.Values(mm.modules))
	mm.mu.Unlock()

	// First, signal graceful shutdown by closing stdin
	for _, instance := range instances {
		instance.logger.Info("closing stdin to request shutdown")
		instance.stdin.Close()
	}

	// Give modules time to shut down gracefully
	time.Sleep(5 * time.Second)

	// Force-remove any containers that are still running
	mm.mu.Lock()
	clear(mm.modules)
	mm.mu.Unlock()

	mm.cleanupOrphanedContainers()
}

// buildPodmanCommand constructs the podman run command for a module
func (mm *ModuleManager) buildPodmanCommand(moduleName, containerName, image string) *exec.Cmd {
	moduleDir := filepath.Join(mm.configManager.shemHome, "modules", moduleName)
	configDir := filepath.Join(moduleDir, "module-config")
	storageDir := filepath.Join(moduleDir, "storage")

	args := []string{
		"run",
		"-i",                    // interactive: keep stdin open for communication
		"--rm",                  // remove container when it exits
		"--replace",             // replace any existing container with the same name
		"--name", containerName, // container name
		"--pull", "never", // do not pull the image, only use it if locally available
		"--network", "none", // no network access
		"--memory", "100m", // memory limit
		"--cpus", "0.1", // CPU limit
		"--read-only",                         // read-only root filesystem
		"--security-opt", "no-new-privileges", // container cannot gain additional privileges
		"--log-driver", "none",                // disable container logging, we read via pipes
	}

	// Mount module-config directory if it exists
	if info, err := os.Stat(configDir); err == nil && info.IsDir() {
		args = append(args, "-v", fmt.Sprintf("%s:/module-config:ro", configDir))
	}

	// Mount storage directory if it exists
	if info, err := os.Stat(storageDir); err == nil && info.IsDir() {
		args = append(args, "-v", fmt.Sprintf("%s:/storage", storageDir))
	}

	// Add image name
	args = append(args, image)

	cmd := exec.Command("podman", args...)

	// Filter out NOTIFY_SOCKET from the environment so podman does not
	// send sd_notify messages to systemd
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "NOTIFY_SOCKET=") {
			cmd.Env = append(cmd.Env, env)
		}
	}

	return cmd
}
