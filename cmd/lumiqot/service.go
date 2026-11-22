package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type managedService struct {
	name, unit         string
	defaultEnabled     bool
	status             func(context.Context) serviceStatus
	drain, stop, start func(context.Context) error
	disarmBoot         func(context.Context) error
}

//go:embed services.json
var serviceManifest []byte

var services = manifestServices(map[string]managedService{
	"worker": {
		name: "worker", unit: workerUnit,
		status: workerReport, drain: drainWorker, stop: stopWorker, start: startWorker,
		disarmBoot: disableWorkerBoot,
	},
	"librarian": {
		name: "librarian", unit: librarianUnit,
		status:     func(ctx context.Context) serviceStatus { return unitReport(ctx, librarianUnit, librarianReady) },
		drain:      func(context.Context) error { return nil },
		stop:       func(ctx context.Context) error { return stopUnit(ctx, librarianUnit) },
		start:      startLibrarian,
		disarmBoot: func(ctx context.Context) error { return disableUnitBoot(ctx, librarianUnit) },
	},
	"gateway": {
		name: "gateway", unit: gatewayUnit,
		status: gatewayReport, drain: drainGateway, stop: func(ctx context.Context) error { return stopUnit(ctx, gatewayUnit) },
		start: startGateway, disarmBoot: func(ctx context.Context) error { return disableUnitBoot(ctx, gatewayUnit) },
	},
})

func manifestServices(handlers map[string]managedService) []managedService {
	var manifest struct {
		Version  int `json:"version"`
		Services []struct {
			ID string `json:"id"`
		} `json:"services"`
	}
	if err := json.Unmarshal(serviceManifest, &manifest); err != nil || manifest.Version != 1 || len(manifest.Services) != len(handlers) {
		panic("invalid embedded services manifest")
	}
	result := make([]managedService, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		handler, ok := handlers[service.ID]
		if !ok {
			panic("unknown or duplicate managed service " + service.ID)
		}
		result = append(result, handler)
		delete(handlers, service.ID)
	}
	return result
}

type serviceStatus struct {
	Desired     string `json:"desired"`
	Actual      string `json:"actual"`
	Health      string `json:"health"`
	Converged   bool   `json:"converged"`
	Load        string `json:"load"`
	Systemd     string `json:"systemd"`
	BootEnabled bool   `json:"boot_enabled"`
	workerControl
	Error string `json:"error,omitempty"`
}

func preferencePath(s managedService) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("service user HOME must be absolute")
	}
	return filepath.Join(home, ".local/state/lumiqot/services", s.name), nil
}

func (s managedService) enabled() (bool, error) {
	path, err := preferencePath(s)
	if err != nil {
		return false, err
	}
	value, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s.defaultEnabled, nil
	}
	if err != nil {
		return false, err
	}
	switch string(value) {
	case "enabled\n":
		return true, nil
	case "disabled\n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid desired state in %s", path)
	}
}

func (s managedService) setEnabled(enabled bool) error {
	path, err := preferencePath(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".preference-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	value := "disabled\n"
	if enabled {
		value = "enabled\n"
	}
	_, writeErr := file.WriteString(value)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func (s managedService) report(ctx context.Context) serviceStatus {
	report := s.status(ctx)
	enabled, err := s.enabled()
	switch {
	case err != nil:
		report.Desired = "unknown"
		report.Error = errors.Join(err, errorText(report.Error)).Error()
	case enabled:
		report.Desired = "enabled"
		report.Converged = report.Actual == "running"
	default:
		report.Desired = "disabled"
		report.Converged = report.Actual == "stopped"
	}
	return report
}

func errorText(value string) error {
	if value == "" {
		return nil
	}
	return errors.New(value)
}

func (s managedService) converge(ctx context.Context) error {
	enabled, err := s.enabled()
	if err != nil {
		return err
	}
	if enabled {
		return s.start(ctx)
	}
	return s.stop(ctx)
}

func serviceCommand(args []string) error {
	if len(args) == 1 && args[0] == "manifest" {
		_, err := os.Stdout.Write(serviceManifest)
		return err
	}
	if len(args) < 2 {
		return errUsage
	}
	action := args[0]
	for _, s := range services {
		if s.name != args[1] {
			continue
		}
		// systemd uses exit 1 to skip ExecStart whenever intent is disabled
		// or unreadable. This path must not take the package lock.
		if action == "check-enabled" && len(args) == 2 {
			enabled, err := s.enabled()
			if err != nil {
				return err
			}
			if !enabled {
				return errors.New("service is disabled")
			}
			return nil
		}
		defaults := map[string]int{"enable": 60, "disable": 120, "status": 8}
		seconds, ok := defaults[action]
		if !ok {
			return errUsage
		}
		flags := flag.NewFlagSet(action, flag.ContinueOnError)
		timeout := flags.Int("timeout", seconds, "operation deadline in seconds (after acquiring the update lock)")
		asJSON := flags.Bool("json", false, "emit JSON status")
		if err := flags.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return err
			}
			return fmt.Errorf("%w: %v", errUsage, err)
		}
		if flags.NArg() != 0 || *timeout < 1 || *timeout > 86400 || (*asJSON && action != "status") {
			return errUsage
		}
		if action == "status" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
			defer cancel()
			status := s.report(ctx)
			if *asJSON {
				return json.NewEncoder(os.Stdout).Encode(status)
			}
			fmt.Printf("%s: desired=%s actual=%s health=%s converged=%t\n",
				s.name, status.Desired, status.Actual, status.Health, status.Converged)
			return nil
		}
		return mutate(append([]string{"service"}, args...), *timeout, func(ctx context.Context) error {
			if err := s.setEnabled(action == "enable"); err != nil {
				return err
			}
			return s.converge(ctx)
		})
	}
	return fmt.Errorf("%w: unknown service %q", errUsage, args[1])
}
