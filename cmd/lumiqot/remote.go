package main

import (
	"context"
	_ "embed"
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
