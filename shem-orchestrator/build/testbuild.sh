#!/bin/bash
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X main.Version=0.0.0" -o shem-orchestrator .
