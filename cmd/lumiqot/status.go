package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

type statusComponent struct {
	State string `json:"state"`
	Value any    `json:"value,omitempty"`
}

type statusSnapshot struct {
	CollectedAt time.Time                  `json:"collectedAt"`
	Components  map[string]json.RawMessage `json:"components"`
}

type statusHistory struct {
	SchemaVersion int `json:"schemaVersion"`
	statusSnapshot
	Previous          *statusSnapshot `json:"previous,omitempty"`
	AgentsError       string          `json:"agentsError,omitempty"`
	ChangedComponents []string        `json:"changedComponents"`
}

func collectStatus(change *githubCLIAccount) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".cache", "lumiqot")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	lock, err := os.OpenFile(filepath.Join(dir, "status.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := waitFor(ctx, func() (bool, error) {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		return err
	}

	var changedAccount *githubCLIAccount
	if change != nil {
		changedAccount, err = githubCLIStatus(ctx, home, change)
		if err != nil {
			return err
		}
	}

	config := filepath.Join(home, ".config", "lumiqot", "worker.json")
	manager := "/opt/lumiqo/manager/current/lumiqo-manager"
	workerSettings, settingsErr := readStatusWorkerSettings(config)
	probes := map[string]func() (any, error){
		"githubCli": func() (any, error) {
			if changedAccount != nil {
				return changedAccount, nil
			}
			return githubCLIStatus(ctx, home, nil)
		},
		"githubApp":        func() (any, error) { return statusGitHubApp(home, config) },
		"updater":          func() (any, error) { return statusUpdater(ctx) },
		"manager":          func() (any, error) { return statusManager(ctx, manager, config) },
		"auth":             func() (any, error) { return statusAuth(ctx) },
		"workerVersion":    func() (any, error) { return version, nil },
		"workerJobs":       func() (any, error) { return statusWorkerJobs(ctx, workerSettings, settingsErr) },
		"network":          func() (any, error) { return statusNetwork(ctx), nil },
		"remote":           func() (any, error) { return statusRemote(ctx, home) },
		"wslIpv4Addresses": func() (any, error) { return statusAddresses(ctx) },
		"gateway":          func() (any, error) { return statusGateway(ctx, home) },
		"sshClient":        func() (any, error) { return statusSSHClient(ctx, home) },
		"workerSettings":   func() (any, error) { return workerSettings, settingsErr },
	}
	components := make(map[string]json.RawMessage, len(probes))
	var mu sync.Mutex
	var pending sync.WaitGroup
	for name, probe := range probes {
		pending.Go(func() {
			component := statusComponent{State: "unavailable"}
			if value, err := probe(); err == nil && value != nil {
				component = statusComponent{State: "available", Value: value}
			}
			data, err := json.Marshal(component)
			if err != nil {
				data = []byte(`{"state":"unavailable"}`)
			}
			mu.Lock()
			components[name] = data
			mu.Unlock()
		})
	}
	pending.Wait()

	name := filepath.Join(dir, "status.json")
	history := statusHistory{
		SchemaVersion:     1,
		statusSnapshot:    statusSnapshot{CollectedAt: time.Now().UTC(), Components: components},
		ChangedComponents: []string{},
	}
	data, err := os.ReadFile(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var old statusHistory
	if err == nil && json.Unmarshal(data, &old) == nil && old.SchemaVersion == history.SchemaVersion {
		history.Previous = old.Previous
		history.ChangedComponents = old.ChangedComponents
		if changes := statusChanges(old.Components, components); len(changes) != 0 {
			history.Previous = &old.statusSnapshot
			history.ChangedComponents = changes
		}
	}
	if err := updateAgents(home, history); err != nil {
		history.AgentsError = err.Error()
	}
	if err := writeStatusJSON(dir, name, history); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(history)
}

func statusChanges(previous, current map[string]json.RawMessage) []string {
	var changes []string
	for name, value := range current {
		// Compact the cached JSON before comparing it with freshly marshalled JSON.
		var compact bytes.Buffer
		if json.Compact(&compact, previous[name]) != nil || !bytes.Equal(compact.Bytes(), value) {
			changes = append(changes, name)
		}
	}
	for name := range previous {
		if _, exists := current[name]; !exists {
			changes = append(changes, name)
		}
	}
	slices.Sort(changes)
	return changes
}

func writeStatusJSON(dir, name string, value any) error {
	file, err := os.CreateTemp(dir, ".status-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(value)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}
