package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Orchestrator struct {
	shemHome        string
	verificationRun bool
	cancel          context.CancelFunc
	logger          *Logger
	configManager   *ConfigManager
	updateManager   *UpdateManager
}

// NewOrchestrator creates a new orchestrator instance
func NewOrchestrator(shemHome string, verificationRun bool) (*Orchestrator, error) {
	logger := NewLogger("orchestrator")

	// Initialize configuration manager
	configManager := NewConfigManager(shemHome)

	// Initialize update manager
	updateManager := NewUpdateManager(configManager, verificationRun)

	return &Orchestrator{
		shemHome:        shemHome,
		configManager:   configManager,
		logger:          logger,
		updateManager:   updateManager,
		verificationRun: verificationRun,
	}, nil
}

// runs the orchestrator; will return only after orchestrator stops
func (o *Orchestrator) Run() {
	o.logger.Info("starting SHEM orchestrator version %s", Version)

	// Create context and WaitGroup for coordinated shutdown
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel

	var wg sync.WaitGroup

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start services
	wg.Go(func() {
		o.updateManager.Run(ctx, cancel)
	})

	if heartbeatService, err := NewHeartbeatService(); err == nil {
		wg.Go(func() {
			heartbeatService.Run(ctx)
		})
	} else {
		o.logger.Info("systemd watchdog not available: %v", err)
	}

	if o.verificationRun {
		// after 10 minutes run verification
		wg.Go(func() {
			select {
			case <-time.After(10 * time.Minute):
				o.VerificationRunCheck()
			case <-ctx.Done():
				return
			}
		})
	}

	// Wait for shutdown signal or context cancellation
	select {
	case <-sigChan:
		o.logger.Info("received shutdown signal, stopping orchestrator...")
		o.cancel()
	case <-ctx.Done():
		o.logger.Info("orchestrator shutdown requested...")
	}

	// wait for services to finish
	wg.Wait()

	o.logger.Info("orchestrator stopped")
}

// Shutdown gracefully shuts down the orchestrator
func (o *Orchestrator) Shutdown() {
	o.logger.Info("shutting down orchestrator...")

	if o.cancel != nil {
		o.cancel()
	} else {
		o.logger.Error("cancel context is nil")
		os.Exit(1)
	}
}

// RunHealthCheck performs health checks for verification runs
func (o *Orchestrator) RunHealthCheck() error {
	// currently does nothing

	return nil
}

func (o *Orchestrator) VerificationRunCheck() {
	if err := o.RunHealthCheck(); err != nil {
		o.logger.Error("health check failed: %v", err)
		os.Exit(1)
	}

	o.logger.Info("verification run successful, removing blacklist entry")
	// remove blacklist entry
	orchestratorConfig, err := o.configManager.NewModuleConfig("orchestrator")
	if err != nil {
		o.logger.Error("failed to get orchestrator config: %v", err)
	} else {
		if err := orchestratorConfig.RemoveFromBlacklist(Version); err != nil {
			o.logger.Error("failed to remove version %s from orchestrator blacklist: %v", Version, err)
		}
	}

	// update symlink to point to this version
	targetBinary := filepath.Join(o.shemHome, "bin", fmt.Sprintf("shem-orchestrator-%s", Version))
	o.logger.Info("updating symlink to point to %s", targetBinary)
	symlinkPath := filepath.Join(o.shemHome, "bin", "shem-orchestrator")
	tempSymlinkPath := symlinkPath + ".tmp"

	// Atomically replace the symlink
	if err := os.Symlink(targetBinary, tempSymlinkPath); err != nil {
		o.logger.Error("failed to create temporary symlink: %v", err)
	} else if err := os.Rename(tempSymlinkPath, symlinkPath); err != nil {
		o.logger.Error("failed to replace symlink: %v", err)
		os.Remove(tempSymlinkPath)
	}

	o.logger.Info("verification run completed successfully, shutting down")
	o.Shutdown()
}
