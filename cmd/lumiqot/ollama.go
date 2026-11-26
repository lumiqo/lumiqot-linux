package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const ollamaURL = "http://127.0.0.1:11434"
const ollamaJobUnit = "lumiqot-ollama-operation.service"

var ollamaModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*(?::[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

func validOllamaModel(name string) bool {
	return len(name) <= 200 && ollamaModelPattern.MatchString(name) && !strings.Contains(name, "..")
}

type ollamaJob struct {
	Action    string    `json:"action"`
	Model     string    `json:"model,omitempty"`
	State     string    `json:"state"`
	Status    string    `json:"status"`
	Completed int64     `json:"completed,omitempty"`
	Total     int64     `json:"total,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ollamaReport struct {
	Installed  bool              `json:"installed"`
	Running    bool              `json:"running"`
	Version    string            `json:"version,omitempty"`
	Models     []json.RawMessage `json:"models"`
	Loaded     []json.RawMessage `json:"loaded"`
	Job        *ollamaJob        `json:"job,omitempty"`
	Diagnostic string            `json:"diagnostic,omitempty"`
}

func ollamaStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local/state/lumiqot/ollama")
	return dir, os.MkdirAll(dir, 0700)
}

func writeOllamaJob(dir string, job ollamaJob) error {
	job.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	// The worker is the only writer while its unit is active. The controller writes
	// before starting or after stopping the unit.
	temp := filepath.Join(dir, "operation.json.tmp")
	if err = os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(dir, "operation.json"))
}

func readOllamaJob(dir string) *ollamaJob {
	data, err := os.ReadFile(filepath.Join(dir, "operation.json"))
	var job ollamaJob
	if err != nil || json.Unmarshal(data, &job) != nil {
		return nil
	}
	return &job
}

func ollamaRequest(ctx context.Context, client *http.Client, base, method, endpoint string, body any, target any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+endpoint, input)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Ollama HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target)
}

func localOllamaClient() *http.Client {
	// Local service requests should never be routed through an HTTP proxy.
	return &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func inspectOllama(ctx context.Context, client *http.Client, base string) ollamaReport {
	report := ollamaReport{Models: []json.RawMessage{}, Loaded: []json.RawMessage{}}
	_, err := exec.LookPath("ollama")
	report.Installed = err == nil
	var version struct {
		Version string `json:"version"`
	}
	if err := ollamaRequest(ctx, client, base, "GET", "/api/version", nil, &version); err != nil {
		if report.Installed {
			report.Diagnostic = "Ollama is installed but its local API is unavailable. Start the service or check its logs."
		}
		return report
	}
	report.Installed, report.Running, report.Version = true, true, version.Version
	var models, loaded struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := ollamaRequest(ctx, client, base, "GET", "/api/tags", nil, &models); err != nil {
		report.Diagnostic = err.Error()
	} else if models.Models != nil {
		report.Models = models.Models
	}
	if err := ollamaRequest(ctx, client, base, "GET", "/api/ps", nil, &loaded); err != nil {
		if report.Diagnostic == "" {
			report.Diagnostic = err.Error()
		}
	} else if loaded.Models != nil {
		report.Loaded = loaded.Models
	}
	return report
}

func ollamaStatus(ctx context.Context) (ollamaReport, error) {
	client := localOllamaClient()
	defer client.CloseIdleConnections()
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	report := inspectOllama(probe, client, ollamaURL)
	dir, err := ollamaStateDir()
	if err != nil {
		return report, err
	}
	report.Job = readOllamaJob(dir)
	if report.Job != nil && report.Job.State == "running" {
		state, err := readUnitState(ctx, ollamaJobUnit)
		if err != nil {
			return report, err
		}
		if state.active != "active" && state.active != "activating" {
			report.Job.State = "failed"
			report.Job.Status = "Operation interrupted. Check Ollama logs and retry."
		}
	}
	return report, nil
}

func ollamaCommand(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	action := args[0]
	if action == "_run" && (len(args) == 2 || len(args) == 3) {
		model := ""
		if len(args) == 3 {
			model = args[2]
		}
		if args[1] != "install" && (args[1] != "pull" || !validOllamaModel(model)) {
			return errUsage
		}
		return runOllamaJob(args[1], model)
	}
	model := ""
	switch action {
	case "status", "install", "start", "stop", "cancel":
		if len(args) != 1 {
			return errUsage
		}
	case "pull", "delete", "test":
		if len(args) != 2 || !validOllamaModel(args[1]) {
			return errUsage
		}
		model = args[1]
	default:
		return errUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	dir, err := ollamaStateDir()
	if err != nil {
		return err
	}
	if action != "status" {
		lock, err := os.OpenFile(filepath.Join(dir, "control.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return errors.New("Another Ollama operation is being started. Try again.")
		}
		report, err := ollamaStatus(ctx)
		if err != nil {
			return err
		}
		busy := report.Job != nil && report.Job.State == "running"
		if busy && action != "cancel" {
			return errors.New("Wait for the current Ollama operation or cancel it first.")
		}
		switch action {
		case "install", "pull":
			if action == "pull" && !report.Running {
				return errors.New("Start Ollama before downloading a model.")
			}
			job := ollamaJob{Action: action, Model: model, State: "running", Status: "Starting…"}
			if err := writeOllamaJob(dir, job); err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			// A transient service keeps long downloads alive when the settings panel or
			// Electron closes. systemd also owns cancellation of all child processes.
			command := []string{"--user", "--collect", "--unit=" + ollamaJobUnit, "--property=Type=exec", "--setenv=HOME=" + os.Getenv("HOME"), "--setenv=PATH=" + os.Getenv("PATH"), "--", executable, "ollama", "_run", action}
			if model != "" {
				command = append(command, model)
			}
			out, err := exec.CommandContext(ctx, "systemd-run", command...).CombinedOutput()
			if err != nil {
				job.State, job.Status = "failed", strings.TrimSpace(string(out))
				_ = writeOllamaJob(dir, job)
				return fmt.Errorf("Could not start Ollama operation: %w: %s", err, out)
			}
		case "cancel":
			if busy {
				if _, err := systemctl(ctx, "stop", ollamaJobUnit); err != nil {
					return err
				}
				job := *report.Job
				job.State, job.Status = "cancelled", "Cancelled. You can retry the operation."
				if err := writeOllamaJob(dir, job); err != nil {
					return err
				}
			}
		case "start", "stop":
			verb := "enable"
			if action == "stop" {
				verb = "disable"
			}
			if out, err := exec.CommandContext(ctx, "sudo", "-n", "systemctl", verb, "--now", "ollama.service").CombinedOutput(); err != nil {
				return fmt.Errorf("Ollama service %s: %w: %s", action, err, out)
			}
			if action == "start" {
				if err := waitFor(ctx, func() (bool, error) {
					client := localOllamaClient()
					defer client.CloseIdleConnections()
					probe, cancel := context.WithTimeout(ctx, time.Second)
					defer cancel()
					var version any
					return ollamaRequest(probe, client, ollamaURL, "GET", "/api/version", nil, &version) == nil, nil
				}); err != nil {
					return err
				}
			}
		case "delete", "test":
			if !report.Running {
				return errors.New("Start Ollama first.")
			}
			client := localOllamaClient()
			defer client.CloseIdleConnections()
			if action == "delete" {
				if err := ollamaRequest(ctx, client, ollamaURL, "DELETE", "/api/delete", map[string]any{"model": model}, nil); err != nil {
					return err
				}
			} else {
				var result struct {
					Done bool `json:"done"`
				}
				body := map[string]any{"model": model, "prompt": "Reply with OK.", "stream": false, "keep_alive": 0, "options": map[string]any{"num_predict": 16, "num_ctx": 2048}}
				if err := ollamaRequest(ctx, client, ollamaURL, "POST", "/api/generate", body, &result); err != nil {
					return err
				}
				if !result.Done {
					return errors.New("Ollama did not complete the test request.")
				}
			}
		}
	}
	report, err := ollamaStatus(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

func runOllamaJob(action, model string) error {
	dir, err := ollamaStateDir()
	if err != nil {
		return err
	}
	job := ollamaJob{Action: action, Model: model, State: "running"}
	update := func(status string) { job.Status = status; _ = writeOllamaJob(dir, job) }
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	if action == "install" {
		update("Downloading and installing Ollama. This may take several minutes.")
		err = installOllama(ctx, dir)
	} else {
		update("Connecting to the model registry…")
		client := localOllamaClient()
		defer client.CloseIdleConnections()
		err = pullOllama(ctx, client, ollamaURL, model, func(status string, completed, total int64) {
			job.Status, job.Completed, job.Total = status, completed, total
			_ = writeOllamaJob(dir, job)
		})
	}
	job.State, job.Status = "succeeded", "Completed."
	if err != nil {
		job.State, job.Status = "failed", err.Error()
	}
	if writeErr := writeOllamaJob(dir, job); writeErr != nil {
		return writeErr
	}
	return err
}

func pullOllama(ctx context.Context, client *http.Client, base, model string, progress func(string, int64, int64)) error {
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Model download returned HTTP %d", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	success := false
	lastWrite := time.Time{}
	for scanner.Scan() {
		var record struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			Completed int64  `json:"completed"`
			Total     int64  `json:"total"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return err
		}
		if record.Error != "" {
			return errors.New(record.Error)
		}
		success = record.Status == "success"
		if success || record.Total == 0 || time.Since(lastWrite) >= 250*time.Millisecond {
			progress(record.Status, record.Completed, record.Total)
			lastWrite = time.Now()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !success {
		return errors.New("Model download ended before completion. Retry to resume downloading.")
	}
	return nil
}

func installOllama(ctx context.Context, dir string) error {
	// Use Ollama's official installer for the runtime, GPU libraries and system
	// service. No model is pulled by installation.
	temp, err := os.MkdirTemp(dir, "install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	log, err := os.OpenFile(filepath.Join(dir, "install.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	script := filepath.Join(temp, "install.sh")
	commands := [][]string{
		{"sudo", "-n", "apt-get", "install", "-y", "curl", "zstd"},
		{"curl", "--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--proto-redir", "=https", "--output", script, "https://ollama.com/install.sh"},
		{"sh", script},
		{"sudo", "-n", "mkdir", "-p", "/etc/systemd/system/ollama.service.d"},
	}
	for _, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Ollama installation failed (%s). See %s/install.log: %w", args[0], dir, err)
		}
	}
	// Explicitly keep the local API on loopback when the installer is rerun.
	override := exec.CommandContext(ctx, "sudo", "-n", "tee", "/etc/systemd/system/ollama.service.d/10-lumiqot.conf")
	override.Stdin = strings.NewReader("[Service]\nEnvironment=OLLAMA_HOST=127.0.0.1:11434\n")
	override.Stdout, override.Stderr = log, log
	if err := override.Run(); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "ollama.service"}, {"restart", "ollama.service"}} {
		cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "systemctl"}, args...)...)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}
