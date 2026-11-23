package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

var version = "dev"

const usage = `Usage:
  lumiqot status [--json]
  lumiqot version
  lumiqot codex-bridge
  lumiqot service {enable|disable|status} SERVICE [--timeout SECONDS] [--json]
  lumiqot service manifest
  lumiqot lifecycle {drain|stop|start|restart|status} [--timeout SECONDS] [--json]

Services: worker, librarian, gateway (disabled by default)
Service enable and disable save intent. Lifecycle commands preserve intent.
Use --json with status.
`

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "lumiqot:", err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(124)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 0 || slices.Equal(args, []string{"--help"}) ||
		slices.Equal(args, []string{"-h"}) || slices.Equal(args, []string{"help"}):
		fmt.Print(usage)
		return nil
	case slices.Equal(args, []string{"version"}), slices.Equal(args, []string{"--version"}):
		fmt.Println(version)
		return nil
	case slices.Equal(args, []string{"status"}), slices.Equal(args, []string{"status", "--json"}):
		return collectStatus()
	case slices.Equal(args, []string{"codex-bridge"}):
		return codexBridge()
	case slices.Equal(args, []string{"service", "--help"}), slices.Equal(args, []string{"lifecycle", "--help"}):
		fmt.Print(usage)
		return nil
	case len(args) > 0 && args[0] == "service":
		return serviceCommand(args[1:])
	case len(args) > 0 && args[0] == "lifecycle":
		return lifecycle(args[1:])
	case len(args) > 0 && args[0] == "run-worker":
		return workerExec(args[1:])
	}
	return fmt.Errorf("%w: unknown command; see lumiqot --help", errUsage)
}

func workerExec(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	worker := filepath.Join(filepath.Dir(executable), "..", "libexec", "lumiqot", "lumiqo-worker")
	return syscall.Exec(worker, append([]string{worker}, args...), os.Environ())
}
