package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed lumiqot-remote.service
var remoteUnitFile []byte

const remoteUnit = "lumiqot-remote.service"

func remoteReady(ctx context.Context, state unitState) (bool, error) {
	return mcpSocketReady(ctx, state, "Remote", "../libexec/lumiqot/lumiqo-remote", "lumiqot-remote/mcp.sock")
}

func startRemote(ctx context.Context) error {
	return startMCPUnit(ctx, remoteUnit, remoteUnitFile, remoteReady)
}

func mcpSocketReady(ctx context.Context, state unitState, name, binary, socket string) (bool, error) {
	if err := verifyServiceExecutable(state.pid, name, binary); err != nil {
		return false, err
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(dir) {
		return false, fmt.Errorf("XDG_RUNTIME_DIR must be absolute")
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", filepath.Join(dir, socket))
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

func startMCPUnit(ctx context.Context, unit string, contents []byte, probe func(context.Context, unitState) (bool, error)) error {
	if err := startUnit(ctx, unit, contents); err != nil {
		return err
	}
	if err := waitForUnitReady(ctx, unit, func(ctx context.Context, state unitState) (bool, error) {
		ready, err := probe(ctx, state)
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			// Type=exec becomes active before the process binds its socket.
			return false, nil
		}
		return ready, err
	}); err != nil {
		return err
	}
	_, err := systemctl(ctx, "enable", "--", unit)
	return err
}
