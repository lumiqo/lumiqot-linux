package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed lumiqot-gateway.service
var gatewayUnitFile []byte

const gatewayUnit = "lumiqot-gateway.service"
const gatewayBinary = "lumiqo-gateway"

func gatewayCommand(ctx context.Context, action string) (bool, error) {
	cmd, err := newGatewayCommand(ctx, action)
	if err != nil {
		return false, err
	}
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
	return unitReport(ctx, gatewayUnit, gatewayReady)
}

func unitReport(ctx context.Context, unit string, ready func(context.Context, unitState) (bool, error)) serviceStatus {
	state, err := readUnitState(ctx, unit)
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
			report.Ready, err = ready(ctx, state)
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

func newGatewayCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	cli, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(filepath.Dir(cli), gatewayBinary), args...)
	// Use the same configuration directory as the systemd user unit.
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	return cmd, nil
}

func gatewayCLI(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: see lumiqot --help", errUsage)
	}
	switch args[0] {
	case "route":
		switch args[1] {
		case "list":
			if len(args) != 2 {
				return errUsage
			}
		case "add":
		case "remove":
			if len(args) != 3 {
				return errUsage
			}
			args = []string{"route", "remove", "--path", args[2]}
		default:
			return errUsage
		}
	case "domain":
		switch args[1] {
		case "list":
			if len(args) != 2 {
				return errUsage
			}
		case "add", "remove":
		default:
			return errUsage
		}
	case "api-key":
		switch args[1] {
		case "list", "issue", "delete":
		default:
			return errUsage
		}
	default:
		return errUsage
	}
	for _, arg := range args[2:] {
		// The wrapper always targets the configuration used by systemd.
		if arg == "-config" || arg == "--config" || strings.HasPrefix(arg, "-config=") || strings.HasPrefix(arg, "--config=") {
			return fmt.Errorf("%w: --config is only available on the standalone Gateway", errUsage)
		}
	}
	cmd, err := newGatewayCommand(context.Background(), args...)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
