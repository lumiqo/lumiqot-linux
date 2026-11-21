package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed lumiqot-worker.service
var workerUnitFile []byte

const workerUnit = "lumiqot-worker.service"
const packageRoot = "/opt/lumiqo/distribution"

var errUsage = errors.New("invalid usage")
var userSystemctl = "/usr/bin/systemctl"

type workerControl struct {
	Ready    bool `json:"ready"`
	Draining bool `json:"draining"`
	Active   int  `json:"active"`
}

type lifecycleStatus struct {
	State     string                   `json:"state"`
	Converged bool                     `json:"converged"`
	Ready     bool                     `json:"ready"`
	Services  map[string]serviceStatus `json:"services"`
}

func lifecycle(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	action := args[0]
	defaults := map[string]int{"drain": 900, "stop": 120, "start": 60, "restart": 960, "status": 8}
	seconds, ok := defaults[action]
	if !ok {
		return fmt.Errorf("%w: unknown lifecycle action %q", errUsage, action)
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	timeout := flags.Int("timeout", seconds, "operation deadline in seconds (after acquiring the update lock)")
	asJSON := flags.Bool("json", false, "emit JSON status")
	if err := flags.Parse(args[1:]); err != nil {
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
		state := applicationStatus(ctx)
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(state)
		}
		fmt.Println(state.State)
		return nil
	}
	return mutate(append([]string{"lifecycle"}, args...), *timeout, func(ctx context.Context) error {
		var result error
		for _, s := range services {
			var err error
			switch action {
			case "drain":
				err = s.drain(ctx)
			case "stop":
				if os.Getenv("LUMIQOT_MANAGER_TRANSACTION") == "1" {
					// Disarm automatic starts before switching the current link.
					err = s.disarmBoot(ctx)
				}
				if err == nil {
					err = s.stop(ctx)
				}
			case "start":
				err = s.converge(ctx)
			case "restart":
				err = s.stop(ctx)
				if err == nil {
					err = s.converge(ctx)
				} else {
					recovery, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					err = errors.Join(err, s.converge(recovery))
					cancel()
				}
			}
			if err != nil {
				result = errors.Join(result, fmt.Errorf("%s: %w", s.name, err))
			}
		}
		return result
	})
}

func mutate(args []string, seconds int, operation func(context.Context) error) error {
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	for _, s := range services {
		if strings.Contains(string(cgroup), "/"+s.unit) {
			return errors.New("invoke lifecycle operations outside the managed service")
		}
	}
	wait, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	lock, err := lockLifecycle(wait, filepath.Join(packageRoot, ".update.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()

	active, err := filepath.EvalSymlinks(filepath.Join(packageRoot, "current/bin/lumiqot"))
	if err != nil {
		return err
	}
	running, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}
	if active != running {
		// A caller queued behind activation must use the new release while
		// retaining the same lock. Manager must always invoke the active CLI.
		if os.Getenv("LUMIQOT_MANAGER_TRANSACTION") == "1" {
			return errors.New("Manager lifecycle call did not use the active package")
		}
		cmd := exec.Command(active, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.ExtraFiles = []*os.File{lock}
		cmd.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
			return strings.HasPrefix(value, "LUMIQOT_INHERITED_LOCK=")
		}), "LUMIQOT_INHERITED_LOCK=1")
		var exit *exec.ExitError
		err := cmd.Run()
		if errors.As(err, &exit) && exit.ExitCode() == 124 {
			return context.DeadlineExceeded
		}
		return err
	}
	ctx, cancelOperation := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancelOperation()
	return operation(ctx)
}

func lockLifecycle(ctx context.Context, path string) (*os.File, error) {
	lock, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if os.Getenv("LUMIQOT_MANAGER_TRANSACTION") == "1" || os.Getenv("LUMIQOT_INHERITED_LOCK") == "1" {
		expected, err := lock.Stat()
		lock.Close()
		if err != nil {
			return nil, err
		}
		lock = os.NewFile(3, path)
		syscall.CloseOnExec(3)
		inherited, err := lock.Stat()
		if err != nil || !os.SameFile(expected, inherited) {
			lock.Close()
			return nil, errors.New("lifecycle call did not inherit the update lock")
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			lock.Close()
			return nil, err
		}
		return lock, nil
	}
	err = waitFor(ctx, func() (bool, error) {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("wait for package or lifecycle operation: %w", err)
	}
	return lock, nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, userSystemctl, append([]string{"--user"}, args...)...).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		err = fmt.Errorf("systemctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), err
}

type unitState struct {
	load, active, enabled, cgroup string
	pid                           int
}

func workerState(ctx context.Context) (unitState, error) {
	out, err := systemctl(ctx, "show", "-p", "LoadState,ActiveState,UnitFileState,MainPID,ControlGroup", "--", workerUnit)
	fields := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	pid, _ := strconv.Atoi(fields["MainPID"])
	state := unitState{fields["LoadState"], fields["ActiveState"], fields["UnitFileState"], fields["ControlGroup"], pid}
	if state.load == "not-found" && state.active == "inactive" {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if state.load == "" || state.active == "" {
		return state, errors.New("systemd returned incomplete unit state")
	}
	return state, nil
}

func verifyWorkerExecutable(pid int) error {
	if pid <= 0 {
		return errors.New("systemd returned no Worker PID")
	}
	running, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return err
	}
	cli, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}
	expected := filepath.Join(filepath.Dir(cli), "..", "libexec/lumiqot/lumiqo-worker")
	if running != expected {
		return fmt.Errorf("running Worker %q differs from %q", running, expected)
	}
	return nil
}

func controlWorker(ctx context.Context, method, action string) (workerControl, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		return workerControl{}, errors.New("XDG_RUNTIME_DIR is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtime, "lumiqot/control.sock"))
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, "http://worker/"+action, nil)
	if err != nil {
		return workerControl{}, err
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return workerControl{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return workerControl{}, fmt.Errorf("Worker %s: %s: %s", action, response.Status, strings.TrimSpace(string(body)))
	}
	var state workerControl
	err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&state)
	return state, err
}

func applicationStatus(ctx context.Context) lifecycleStatus {
	return aggregateStatus(ctx, services)
}

func aggregateStatus(ctx context.Context, managed []managedService) lifecycleStatus {
	status := lifecycleStatus{State: "disabled", Converged: true, Services: map[string]serviceStatus{}}
	// A mixed result exposes every actual state in the services map. Failures
	// take precedence over temporary states in the aggregate.
	severity := map[string]int{"disabled": 0, "healthy": 1, "stopped": 2, "draining": 3, "partial": 4, "failed": 5}
	for _, s := range managed {
		report := s.report(ctx)
		status.Services[s.unit] = report
		status.Converged = status.Converged && report.Converged
		phase := report.Actual
		switch {
		case report.Desired == "unknown":
			phase = "failed"
		case report.Desired == "disabled" && report.Converged:
			phase = "disabled"
		case report.Actual == "running":
			phase = "partial"
			if report.Health == "healthy" && report.Converged && report.BootEnabled {
				phase = "healthy"
			}
		}
		if severity[phase] > severity[status.State] {
			status.State = phase
		}
	}
	status.Ready = status.State == "disabled" || status.State == "healthy"
	return status
}

func workerReport(ctx context.Context) serviceStatus {
	systemd, err := workerState(ctx)
	service := serviceStatus{Actual: "failed", Health: "unknown", Load: systemd.load, Systemd: systemd.active,
		BootEnabled: systemd.enabled == "enabled"}
	if err == nil {
		switch systemd.active {
		case "inactive":
			service.Actual, service.Health = "stopped", "not_applicable"
		case "failed":
			service.Health = "unhealthy"
		case "active":
			service.Actual, service.Health = "running", "unhealthy"
			service.workerControl, err = controlWorker(ctx, http.MethodGet, "status")
			if err == nil && service.Draining {
				service.Actual = "draining"
			}
			if err == nil && service.Ready {
				err = verifyWorkerExecutable(systemd.pid)
				if err == nil {
					service.Health = "healthy"
				}
			}
		default:
			service.Actual = "partial"
		}
	}
	if err != nil {
		service.Error = err.Error()
	}
	return service
}

func waitFor(ctx context.Context, check func() (bool, error)) error {
	for {
		ok, err := check()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ok || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func drainWorker(ctx context.Context) error {
	var lastControlErr error
	armed := false
	err := waitFor(ctx, func() (bool, error) {
		state, err := workerState(ctx)
		if err != nil {
			return false, err
		}
		switch state.active {
		case "inactive", "failed":
			return true, nil
		case "active":
			var control workerControl
			if armed {
				control, lastControlErr = controlWorker(ctx, http.MethodGet, "status")
			} else {
				control, lastControlErr = controlWorker(ctx, http.MethodPost, "drain")
			}
			armed = lastControlErr == nil && control.Draining
			return armed && control.Active == 0, nil
		default:
			return false, nil
		}
	})
	if err != nil && lastControlErr != nil {
		return errors.Join(err, lastControlErr)
	}
	return err
}

func stopWorker(ctx context.Context) error {
	if err := drainWorker(ctx); err != nil {
		return err
	}
	state, err := workerState(ctx)
	if err != nil || state.load == "not-found" {
		return err
	}
	if state.active != "inactive" {
		if _, err := systemctl(ctx, "stop", "--", workerUnit); err != nil {
			return err
		}
		if err := waitFor(ctx, func() (bool, error) {
			state, err := workerState(ctx)
			return (state.active == "inactive" || state.active == "failed") && state.pid == 0, err
		}); err != nil {
			return err
		}
	}
	if err := waitFor(ctx, func() (bool, error) { return emptyControlGroup(state.cgroup) }); err != nil {
		return fmt.Errorf("wait for %s process group: %w", workerUnit, err)
	}
	state, err = workerState(ctx)
	if err == nil && state.active == "failed" {
		_, err = systemctl(ctx, "reset-failed", "--", workerUnit)
	}
	return err
}

func disableWorkerBoot(ctx context.Context) error {
	state, err := workerState(ctx)
	if err != nil || state.load == "not-found" || state.enabled == "disabled" {
		return err
	}
	_, err = systemctl(ctx, "disable", "--", workerUnit)
	return err
}

func emptyControlGroup(group string) (bool, error) {
	if group == "" {
		return true, nil
	}
	if group == "/" || !filepath.IsAbs(group) || filepath.Clean(group) != group {
		return false, fmt.Errorf("unexpected systemd control group %q", group)
	}
	events, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", group, "cgroup.events"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(events), "\n") {
		switch line {
		case "populated 0":
			return true, nil
		case "populated 1":
			return false, nil
		}
	}
	return false, errors.New("systemd control group has no populated flag")
}

func registerWorker(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}
	dir := filepath.Join(configDir, "systemd", "user")
	path := filepath.Join(dir, workerUnit)
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if string(existing) != string(workerUnitFile) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		file, err := os.CreateTemp(dir, ".unit-")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		_, writeErr := file.Write(workerUnitFile)
		if err := errors.Join(writeErr, file.Chmod(0644), file.Close()); err != nil {
			return err
		}
		if err := os.Rename(file.Name(), path); err != nil {
			return err
		}
	}
	_, err = systemctl(ctx, "daemon-reload")
	return err
}

func startWorker(ctx context.Context) error {
	if err := registerWorker(ctx); err != nil {
		return err
	}
	unit, err := workerState(ctx)
	if err != nil {
		return err
	}
	if unit.load == "loaded" && unit.active != "active" {
		// The previous candidate shares this unit's start-rate counter.
		if _, err := systemctl(ctx, "reset-failed", "--", workerUnit); err != nil {
			return err
		}
	}
	if _, err := systemctl(ctx, "start", "--", workerUnit); err != nil {
		return err
	}
	if err := waitFor(ctx, func() (bool, error) {
		state, err := workerState(ctx)
		if err != nil {
			return false, err
		}
		if state.active == "failed" || state.active == "inactive" {
			return false, fmt.Errorf("%s became %s; see journalctl --user -u %s", workerUnit, state.active, workerUnit)
		}
		if state.active != "active" {
			return false, nil
		}
		control, err := controlWorker(ctx, http.MethodGet, "status")
		return err == nil && control.Ready, nil
	}); err != nil {
		return err
	}
	// Confirm the service is running the binary paired with this CLI before
	// enabling admission. In particular, a stale PID cannot satisfy readiness.
	unit, err = workerState(ctx)
	if err != nil {
		return err
	}
	if err := verifyWorkerExecutable(unit.pid); err != nil {
		return err
	}
	if _, err := systemctl(ctx, "enable", "--", workerUnit); err != nil {
		return err
	}
	state, err := controlWorker(ctx, http.MethodPost, "resume")
	if err != nil {
		return err
	}
	if !state.Ready || state.Draining {
		return errors.New("Worker did not resume")
	}
	return nil
}
