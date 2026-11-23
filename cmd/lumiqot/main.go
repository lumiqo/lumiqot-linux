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
  lumiqot github-cli set-account-type LOGIN {personal|work|ai}
  lumiqot version
  lumiqot codex-bridge
  lumiqot worker fetch-all [--config FILE]
  lumiqot worker push-all [--config FILE] [--dry-run]
  lumiqot service {enable|disable|status} SERVICE [--timeout SECONDS] [--json]
  lumiqot service manifest
  lumiqot gateway route list
  lumiqot gateway route add --path PATH --auth POLICY [OPTIONS]
  lumiqot gateway route remove PATH
  lumiqot gateway domain list
  lumiqot gateway domain add --host HOST --upstream URL --auth public [--route-host HOST]
  lumiqot gateway domain remove --host HOST
  lumiqot gateway api-key list
  lumiqot gateway api-key issue --name NAME [--path PATH]
  lumiqot gateway api-key delete --name NAME
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
		return collectStatus(nil)
	case len(args) == 4 && args[0] == "github-cli" && args[1] == "set-account-type":
		if !githubLoginPattern.MatchString(args[2]) || !validGitHubAccountType(args[3]) {
			return errUsage
		}
		return collectStatus(&githubCLIAccount{Login: args[2], AccountType: args[3]})
	case slices.Equal(args, []string{"codex-bridge"}):
		return codexBridge()
	case slices.Equal(args, []string{"service", "--help"}), slices.Equal(args, []string{"lifecycle", "--help"}):
		fmt.Print(usage)
		return nil
	case len(args) > 0 && args[0] == "gateway":
		return gatewayCLI(args[1:])
	case len(args) > 0 && args[0] == "service":
		return serviceCommand(args[1:])
	case len(args) > 0 && args[0] == "lifecycle":
		return lifecycle(args[1:])
	case len(args) > 0 && args[0] == "run-worker":
		return workerExec(args[1:])
	case len(args) > 0 && args[0] == "worker":
		return workerCommand(args[1:])
	}
	return fmt.Errorf("%w: unknown command; see lumiqot --help", errUsage)
}

func workerCommand(args []string) error {
	if slices.Equal(args, []string{"--help"}) || slices.Equal(args, []string{"-h"}) {
		fmt.Print("Usage:\n  lumiqot worker fetch-all [--config FILE]\n  lumiqot worker push-all [--config FILE] [--dry-run]\n")
		return nil
	}
	if len(args) == 0 || (args[0] != "fetch-all" && args[0] != "push-all") {
		return fmt.Errorf("%w: see lumiqot worker --help", errUsage)
	}
	mode := args[0]
	flags := flag.NewFlagSet("worker "+mode, flag.ContinueOnError)
	config := flags.String("config", "", "Worker configuration file (default ~/.config/lumiqot/worker.json)")
	var dryRun bool
	if mode == "push-all" {
		flags.BoolVar(&dryRun, "dry-run", false, "preview push candidates")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected arguments for worker %s", errUsage, mode)
	}
	if *config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		*config = filepath.Join(home, ".config", "lumiqot", "worker.json")
	}
	workerArgs := []string{"-mode", mode, "-config", *config}
	if dryRun {
		workerArgs = append(workerArgs, "-dry-run")
	}
	return workerExec(workerArgs)
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
