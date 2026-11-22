package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed lumiqot-gateway.service
var gatewayUnitFile []byte

const gatewayUnit = "lumiqot-gateway.service"
const gatewayBinary = "lumiqo-gateway"

func gatewayCommand(ctx context.Context, action string) (bool, error) {
	cli, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(filepath.Dir(cli), gatewayBinary), action)
	// Match the user unit even when the caller has a custom XDG directory.
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var result struct {
		OK    bool   `json:"ok"`
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	decodeErr := json.Unmarshal(data, &result)
	if result.Error != "" {
		return false, fmt.Errorf("gateway %s: %s", action, result.Error)
	}
	if err != nil {
		return false, fmt.Errorf("gateway %s: %w", action, err)
	}
	if decodeErr != nil {
		return false, fmt.Errorf("decode gateway %s: %w", action, decodeErr)
	}
	if !result.OK {
		return false, fmt.Errorf("gateway %s did not succeed", action)
	}
	return result.Ready, nil
}

func drainGateway(ctx context.Context) error {
	state, err := readUnitState(ctx, gatewayUnit)
	if err != nil || state.active != "active" {
		return err
	}
	_, err = gatewayCommand(ctx, "drain")
	return err
}

func gatewayReport(ctx context.Context) serviceStatus {
	state, err := readUnitState(ctx, gatewayUnit)
	report := serviceStatus{Actual: "failed", Health: "unknown", Load: state.load, Systemd: state.active,
		BootEnabled: state.enabled == "enabled"}
	if err == nil {
		switch state.active {
		case "inactive":
			report.Actual, report.Health = "stopped", "not_applicable"
		case "failed":
			report.Health = "unhealthy"
		case "active":
			report.Actual, report.Health = "running", "unhealthy"
			report.Ready, err = gatewayReady(ctx, state)
			if report.Ready {
				report.Health = "healthy"
			}
		default:
			report.Actual = "partial"
		}
	}
	if err != nil {
		report.Error = err.Error()
	}
	return report
}

func gatewayReady(ctx context.Context, state unitState) (bool, error) {
	if err := verifyServiceExecutable(state.pid, "Gateway", gatewayBinary); err != nil {
		return false, err
	}
	return gatewayCommand(ctx, "status")
}

func startGateway(ctx context.Context) error {
	if err := startUnit(ctx, gatewayUnit, gatewayUnitFile); err != nil {
		return err
	}
	if err := waitForUnitReady(ctx, gatewayUnit, gatewayReady); err != nil {
		return err
	}
	_, err := systemctl(ctx, "enable", "--", gatewayUnit)
	return err
}
