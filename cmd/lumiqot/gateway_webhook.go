package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const workerWebhookPath = "/hooks/github"

func registerWorkerWebhook(args []string) error {
	flags := flag.NewFlagSet("gateway worker-webhook register", flag.ContinueOnError)
	secret := flags.String("secret-file", "", "absolute path to the private GitHub webhook secret file")
	installation := flags.Int64("installation", 0, "GitHub App installation ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*secret) || *installation <= 0 {
		return fmt.Errorf("%w: supply --secret-file and --installation", errUsage)
	}
	args = []string{"route", "ensure", "--path", workerWebhookPath,
		"--endpoint", "worker.github-webhook", "--auth", "github-webhook",
		"--secret-file", *secret, "--installation", strconv.FormatInt(*installation, 10),
		"--event", "issues", "--event", "issue_comment"}
	cmd, err := newGatewayCommand(context.Background(), args...)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
