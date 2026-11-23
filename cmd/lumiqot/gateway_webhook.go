package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

const workerWebhookPath = "/hooks/github"

func registerWorkerWebhook(args []string) error {
	flags := flag.NewFlagSet("gateway worker-webhook register", flag.ContinueOnError)
	secret := flags.String("secret-file", "", "absolute path to the private GitHub webhook secret file")
	installation := flags.Int64("installation", 0, "GitHub App installation ID")
	var repositories []int64
	flags.Func("repository", "GitHub repository ID (repeatable)", func(value string) error {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("repository ID must be a positive integer")
		}
		repositories = append(repositories, id)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*secret) || *installation <= 0 || len(repositories) == 0 {
		return fmt.Errorf("%w: supply --secret-file, --installation and at least one --repository", errUsage)
	}
	slices.Sort(repositories)
	args = []string{"route", "ensure", "--path", workerWebhookPath,
		"--endpoint", "worker.github-webhook", "--auth", "github-webhook",
		"--secret-file", *secret, "--installation", strconv.FormatInt(*installation, 10),
		"--event", "issues", "--event", "issue_comment"}
	for _, id := range slices.Compact(repositories) {
		args = append(args, "--repository", strconv.FormatInt(id, 10))
	}
	cmd, err := newGatewayCommand(context.Background(), args...)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
