package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func statusCommand(ctx context.Context, seconds int, name string, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, name, args...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

func statusJSON(ctx context.Context, seconds int, into any, name string, args ...string) error {
	data, err := statusCommand(ctx, seconds, name, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func statusAddresses(ctx context.Context) (any, error) {
	var devices []struct {
		Addresses []struct{ Local string } `json:"addr_info"`
	}
	if err := statusJSON(ctx, 8, &devices, "ip", "-j", "-4", "address", "show", "up", "scope", "global"); err != nil {
		return nil, err
	}
	addresses := []string{}
	for _, device := range devices {
		for _, address := range device.Addresses {
			if address.Local != "" {
				addresses = append(addresses, address.Local)
			}
		}
	}
	slices.Sort(addresses)
	return slices.Compact(addresses), nil
}

var sshPublicKey = regexp.MustCompile(`^(ssh-|ecdsa-|sk-)\S+ [A-Za-z0-9+/=]+( [^\r\n]*)?$`)

func statusSSHClient(ctx context.Context, home string) (any, error) {
	keys := []string{}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa", "id_dsa", "id_ed25519_sk", "id_ecdsa_sk"} {
		path := filepath.Join(home, ".ssh", name)
		public, err := os.ReadFile(path + ".pub")
		if errors.Is(err, os.ErrNotExist) {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				public, _ = statusCommand(ctx, 2, "ssh-keygen", "-y", "-P", "", "-f", path)
			}
		} else if err != nil {
			return nil, err
		}
		for line := range strings.Lines(string(public)) {
			line = strings.TrimRight(line, "\r\n")
			if sshPublicKey.MatchString(line) && !slices.Contains(keys, line) {
				keys = append(keys, line)
			}
		}
	}
	result := map[string]any{"publicKeys": keys}
	verified, err := os.ReadFile(filepath.Join(home, ".ssh", ".lumiqot-windows-verified"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(verified))); err == nil {
		result["windowsVerifiedAt"] = at.UTC().Format(time.RFC3339)
	}
	return result, nil
}

func statusGateway(ctx context.Context, home string) (any, error) {
	cli, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var report struct {
		OK     bool
		State  string `json:"state"`
		Config *struct {
			Listen       string `json:"listen"`
			Connectivity struct {
				Provider  string `json:"provider"`
				PublicURL string `json:"public_url,omitempty"`
				TunnelID  string `json:"tunnel_id,omitempty"`
			} `json:"connectivity"`
		} `json:"config"`
		Connectivity *struct {
			State      string `json:"state"`
			Diagnostic string `json:"diagnostic,omitempty"`
		} `json:"connectivity,omitempty"`
	}
	err = statusJSON(ctx, 12, &report, filepath.Join(filepath.Dir(cli), gatewayBinary),
		"connectivity", "show", "-config", filepath.Join(home, ".config", "lumiqo-gateway", "gateway.yaml"))
	if err != nil || !report.OK || report.Config == nil || report.Config.Connectivity.Provider == "" {
		return nil, errors.New("Gateway status unavailable")
	}
	return report, nil
}

func statusNetwork(ctx context.Context) any {
	route, err := statusCommand(ctx, 4, "ip", "-4", "route", "show", "default")
	if err != nil || len(bytes.TrimSpace(route)) == 0 {
		route, err = statusCommand(ctx, 4, "ip", "-6", "route", "show", "default")
		if err != nil || len(bytes.TrimSpace(route)) == 0 {
			return map[string]string{"state": "Offline", "detail": "WSL にインターネット向けの既定経路がありません。"}
		}
	}
	var failures []string
	for _, host := range []string{"www.github.com", "chatgpt.com"} {
		data, err := statusCommand(ctx, 7, "curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5", "https://"+host+"/")
		code := strings.TrimSpace(string(data))
		if err != nil || len(code) != 3 || code < "200" || code >= "500" {
			failures = append(failures, fmt.Sprintf("%s (HTTP %s)", host, code))
		}
	}
	if len(failures) != 0 {
		return map[string]string{"state": "Error", "detail": strings.Join(failures, " / ")}
	}
	return map[string]string{"state": "Online", "detail": "GitHub と Codex の HTTPS 到達性を確認しました。"}
}

func statusManager(ctx context.Context, manager, config string) (any, error) {
	var report struct {
		Version      string
		Distribution json.RawMessage
	}
	if err := statusJSON(ctx, 35, &report, manager, "status", "--json"); err != nil {
		return nil, err
	}
	var distribution struct {
		Protocol      int    `json:"lifecycle_protocol"`
		Installed     string `json:"installed_version"`
		Pending       json.RawMessage
		Integrity     string
		LastError     string `json:"last_error"`
		RecoveryError string `json:"recovery_error"`
		Lifecycle     *lifecycleStatus
	}
	if err := json.Unmarshal(report.Distribution, &distribution); err != nil || distribution.Protocol != 2 {
		return nil, errors.New("unsupported Manager lifecycle report")
	}
	configInfo, err := os.Stat(config)
	configured := err == nil && configInfo.Size() > 0
	installed := distribution.Installed != ""
	state := "uninstalled"
	if installed {
		state = "unknown"
	}
	var worker serviceStatus
	if distribution.Lifecycle != nil {
		state = distribution.Lifecycle.State
		worker = distribution.Lifecycle.Services[workerUnit]
	}
	if worker.Actual != "" {
		state = worker.Actual
	}
	if worker.Load == "" {
		worker.Load = "unknown"
	}
	stopped := worker.Desired == "disabled"
	intentionallyStopped := stopped && worker.Actual == "stopped"
	if intentionallyStopped {
		state = "disabled"
	}
	var healthy any
	switch worker.Health {
	case "healthy":
		healthy = true
	case "unhealthy":
		healthy = false
	}
	return map[string]any{
		"version": report.Version, "workerConfigured": configured,
		"distributionInstalled": installed, "distribution": report.Distribution,
		"installationFailed": (len(distribution.Pending) > 0 && string(distribution.Pending) != "null") ||
			distribution.LastError != "" || distribution.RecoveryError != "" || distribution.Integrity == "damaged",
		"worker": map[string]any{
			"load": worker.Load, "active": worker.Systemd, "state": state,
			"desired": worker.Desired, "actual": worker.Actual, "health": worker.Health, "converged": worker.Converged,
			"running": worker.Actual == "running" || worker.Actual == "draining", "healthy": healthy,
			"draining": worker.Actual == "draining", "stopRequested": stopped, "intentionallyStopped": intentionallyStopped,
		},
	}, nil
}

// Keep this projection limited to Worker settings that contain no credentials.
type statusWorkerSettings struct {
	Concurrency             int `json:"concurrency"`
	PollSeconds             int `json:"poll_seconds"`
	TimeoutSeconds          int `json:"timeout_seconds"`
	LeaseSeconds            int `json:"lease_seconds"`
	MaxAttempts             int `json:"max_attempts"`
	FinalizationMaxAttempts int `json:"finalization_max_attempts"`
}

func readStatusWorkerSettings(config string) (*statusWorkerSettings, error) {
	data, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}
	var settings *statusWorkerSettings
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		return nil, errors.New("invalid Worker settings")
	}
	return settings, nil
}

func statusWorkerJobs(ctx context.Context, settings *statusWorkerSettings, settingsErr error) (any, error) {
	if settingsErr != nil {
		return nil, settingsErr
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	worker := workerReport(bounded)
	if !worker.Ready {
		return nil, nil
	}
	if settings.Concurrency < 0 || settings.Concurrency > 16 {
		return nil, errors.New("invalid Worker concurrency")
	}
	return map[string]any{
		"count": worker.Active, "capacity": max(settings.Concurrency, 1), "jobs": []any{}, "lastResult": nil,
	}, nil
}

func statusAuth(ctx context.Context) (any, error) {
	type authProbe struct {
		args      []string
		signedOut func(string) bool
	}
	probes := map[string]authProbe{
		"codex": {[]string{"login", "status"}, func(message string) bool { return message == "not logged in" }},
		"gh": {[]string{"auth", "status", "--hostname", "github.com"}, func(message string) bool {
			return strings.Contains(message, "not logged into any github hosts")
		}},
		"gcloud": {[]string{"auth", "list", "--filter=status:ACTIVE", "--format=value(account)"}, nil},
		"ntn": {[]string{"whoami"}, func(message string) bool {
			return strings.Contains(message, "not logged in") || strings.Contains(message, "not authenticated")
		}},
	}
	type authResult struct{ name, state string }
	results := make(chan authResult, len(probes))
	for name, probe := range probes {
		go func() {
			if _, err := exec.LookPath(name); err != nil {
				results <- authResult{name, "missing"}
				return
			}
			output, err := statusCommand(ctx, 8, name, probe.args...)
			state := "unverified"
			switch {
			case err == nil && name == "gcloud" && len(bytes.TrimSpace(output)) == 0:
				state = "signed_out"
			case err == nil:
				state = "signed_in"
			default:
				var exit *exec.ExitError
				if probe.signedOut != nil && errors.As(err, &exit) && (name == "ntn" || exit.ExitCode() == 1) &&
					probe.signedOut(strings.ToLower(strings.TrimSpace(string(output)+"\n"+string(exit.Stderr)))) {
					state = "signed_out"
				}
			}
			results <- authResult{name, state}
		}()
	}
	states := make(map[string]string, len(probes))
	for range probes {
		result := <-results
		states[result.name] = result.state
	}
	return states, nil
}

func statusRemote(ctx context.Context, home string) (any, error) {
	_, err := statusCommand(ctx, 8, "codex", "remote-control", "pair", "--help")
	result := map[string]bool{"supported": err == nil, "running": false}
	if err != nil {
		return result, nil
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	var daemon struct{ AppServerVersion *string }
	data, err := statusCommand(ctx, 8, "codex", "app-server", "daemon", "version")
	if err != nil {
		socket := filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
		data, err := os.ReadFile("/proc/net/unix")
		if err != nil {
			return nil, err
		}
		for line := range strings.Lines(string(data)) {
			fields := strings.Fields(line)
			if len(fields) >= 8 && fields[3] == "00010000" && strings.Join(fields[7:], " ") == socket {
				return nil, errors.New("Codex daemon status unavailable")
			}
		}
		return result, nil
	}
	if err := json.Unmarshal(data, &daemon); err != nil {
		return nil, err
	}
	if daemon.AppServerVersion == nil {
		return result, nil
	}
	data, err = os.ReadFile(filepath.Join(codexHome, "app-server-daemon", "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	var settings struct{ RemoteControlEnabled bool }
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	result["running"] = settings.RemoteControlEnabled
	return result, nil
}

func statusGitHubApp(home, workerConfig string) (any, error) {
	path := filepath.Join(home, ".config", "lumiqot", "github-app.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{"configured": false}, nil
	}
	if err != nil {
		return nil, err
	}
	var app struct {
		ID             int64  `json:"app_id"`
		Slug           string `json:"app_slug"`
		PEM            string `json:"private_key_pem"`
		WorkflowsWrite bool   `json:"workflows_write"`
	}
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, err
	}

	var installation any
	data, err = os.ReadFile(workerConfig)
	if err == nil {
		var worker struct {
			GitHub struct {
				Auth           string
				AppID          json.RawMessage `json:"app_id"`
				Slug           string          `json:"app_slug"`
				PrivateKeyFile string          `json:"private_key_file"`
				InstallationID json.RawMessage `json:"installation_id"`
			}
		}
		key := fmt.Sprintf("github-app-%d-%x.pem", app.ID, sha256.Sum256([]byte(app.PEM)))
		if json.Unmarshal(data, &worker) == nil && worker.GitHub.Auth == "app" &&
			strings.Trim(string(worker.GitHub.AppID), `"`) == strconv.FormatInt(app.ID, 10) &&
			worker.GitHub.Slug == app.Slug && worker.GitHub.PrivateKeyFile == filepath.Join(filepath.Dir(path), key) {
			if id, err := strconv.ParseInt(strings.Trim(string(worker.GitHub.InstallationID), `"`), 10, 64); err == nil && id > 0 {
				installation = id
			}
		}
	}
	return map[string]any{
		"configured": true, "appId": app.ID, "slug": app.Slug,
		"installationId": installation, "workflowsWrite": app.WorkflowsWrite,
	}, nil
}

func statusUpdater(ctx context.Context) (any, error) {
	data, _ := statusCommand(ctx, 8, "sha256sum", "/opt/lumiqo/updater/lumiqo-updater")
	digest, _, _ := strings.Cut(string(data), " ")
	timer, err := statusCommand(ctx, 8, "systemctl", "show", "lumiqo-updater.timer", "--property=ActiveState,LastTriggerUSec")
	if err != nil {
		return nil, err
	}
	service, err := statusCommand(ctx, 8, "systemctl", "show", "lumiqo-updater.service", "--property=Result")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"digest": digest, "timer": strings.TrimSpace(string(timer)), "service": strings.TrimSpace(string(service)),
	}, nil
}
