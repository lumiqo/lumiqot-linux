package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

const usage = `Usage:
  lumiqot version
  lumiqot worker status [--json] [--user] [--unit NAME]
  lumiqot worker exec -- ARGS...
`

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "lumiqot:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 0 || slices.Equal(args, []string{"--help"}) ||
		slices.Equal(args, []string{"-h"}) || slices.Equal(args, []string{"help"}) ||
		slices.Equal(args, []string{"worker", "--help"}):
		fmt.Print(usage)
		return nil
	case slices.Equal(args, []string{"version"}), slices.Equal(args, []string{"--version"}):
		fmt.Println(version)
		return nil
	case len(args) >= 2 && args[0] == "worker":
		switch args[1] {
		case "status":
			return workerStatus(args[2:])
		case "exec":
			return workerExec(args[2:])
		}
	}
	return errors.New("unknown command; see lumiqot --help")
}

func workerExec(args []string) error {
	if len(args) < 2 || args[0] != "--" {
		return errors.New("expected lumiqot worker exec -- ARGS; use -- -help for worker options")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	worker := filepath.Join(filepath.Dir(executable), "..", "libexec", "lumiqot", "lumiqo-worker")
	return syscall.Exec(worker, append([]string{worker}, args[1:]...), os.Environ())
}

type serviceStatus struct {
	Unit   string `json:"unit"`
	Scope  string `json:"scope"`
	Load   string `json:"load"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
}

func workerStatus(args []string) error {
	flags := flag.NewFlagSet("worker status", flag.ContinueOnError)
	unit := flags.String("unit", "lumiqo-worker.service", "systemd unit to query")
	user := flags.Bool("user", false, "query the user systemd manager")
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *unit == "" {
		return errors.New("expected worker status [--json] [--user] [--unit NAME]")
	}

	status := serviceStatus{Unit: *unit, Scope: "system"}
	systemctlArgs := []string{"show", "--property=LoadState,ActiveState,SubState", "--", *unit}
	if *user {
		status.Scope = "user"
		systemctlArgs = append([]string{"--user"}, systemctlArgs...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "systemctl", systemctlArgs...).Output()
	for _, line := range strings.Split(string(output), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "LoadState":
			status.Load = value
		case "ActiveState":
			status.Active = value
		case "SubState":
			status.Sub = value
		}
	}
	if err != nil && status.Load != "not-found" {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) != 0 {
			return fmt.Errorf("systemctl: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("systemctl: %w", err)
	}
	if status.Load == "" || status.Active == "" || status.Sub == "" {
		return errors.New("systemctl returned incomplete service state")
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	_, err = fmt.Printf("%s (%s): %s (%s), load=%s\n", status.Unit, status.Scope, status.Active, status.Sub, status.Load)
	return err
}
