package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

type HeartbeatService struct {
	logger       *Logger
	notifySocket string
	interval     time.Duration
}

// NewHeartbeatService creates a new systemd heartbeat service
func NewHeartbeatService() (*HeartbeatService, error) {
	logger := NewLogger("orchestrator-heartbeat")

	// Check if systemd watchdog is enabled
	notifySocket := os.Getenv("NOTIFY_SOCKET")
	if notifySocket == "" {
		return nil, fmt.Errorf("systemd watchdog not enabled (NOTIFY_SOCKET not set)")
	}

	// Get watchdog timeout from environment
	watchdogUsecStr := os.Getenv("WATCHDOG_USEC")
	if watchdogUsecStr == "" {
		return nil, fmt.Errorf("systemd watchdog not configured (WATCHDOG_USEC not set)")
	}

	watchdogUsec, err := strconv.ParseInt(watchdogUsecStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid WATCHDOG_USEC value: %s", watchdogUsecStr)
	}

	// Calculate heartbeat interval (half of watchdog timeout for safety)
	interval := time.Duration(watchdogUsec/2) * time.Microsecond

	return &HeartbeatService{
		logger:       logger,
		notifySocket: notifySocket,
		interval:     interval,
	}, nil
}

// Run sends heartbeats until the context is canceled
func (hs *HeartbeatService) Run(ctx context.Context) {
	hs.logger.Info("starting systemd heartbeat service with %v interval", hs.interval)

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		hs.logger.Error("failed to create heartbeat socket: %v", err)
		return
	}
	defer syscall.Close(fd)

	addr := &syscall.SockaddrUnix{Name: hs.notifySocket}
	message := []byte("WATCHDOG=1")

	ticker := time.NewTicker(hs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := syscall.Sendto(fd, message, 0, addr); err != nil {
				hs.logger.Error("failed to send heartbeat: %v", err)
			} else {
				hs.logger.Debug("sent heartbeat to systemd watchdog")
			}
		case <-ctx.Done():
			hs.logger.Info("stopping heartbeat service")
			return
		}
	}
}
