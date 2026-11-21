package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func codexBridge() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	socketPath, err := startCodexDaemon(ctx, os.Stderr)
	if err != nil {
		return err
	}
	return bridgeCodexSocket(ctx, socketPath, os.Stdin, os.Stdout)
}

func startCodexDaemon(ctx context.Context, diagnostics io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "app-server", "daemon", "start")
	cmd.Stderr = diagnostics
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", fmt.Errorf("start Codex app-server daemon: %w", err)
	}

	var result struct {
		SocketPath string `json:"socketPath"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("decode Codex daemon start output: %w", err)
	}
	if result.SocketPath == "" {
		return "", errors.New("Codex daemon start did not return socketPath")
	}
	return result.SocketPath, nil
}

func bridgeCodexSocket(ctx context.Context, path string, input io.Reader, output io.Writer) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	socket, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("connect to Codex daemon socket: %w", err)
	}
	conn := socket.(*net.UnixConn)
	defer conn.Close()

	// These copies block on their destination, keeping buffered data bounded.
	// A pipe on either side can remain blocked after disconnect or cancellation.
	// Buffered results let the command exit without waiting for that pipe.
	sent, received := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, input)
		if err == nil {
			err = conn.CloseWrite()
		}
		sent <- err
	}()
	go func() {
		_, err := io.Copy(output, conn)
		received <- err
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sent:
			if err != nil {
				return fmt.Errorf("relay stdin to Codex daemon: %w", err)
			}
			// Keep draining replies after stdin's EOF.
			sent = nil
		case err := <-received:
			if err != nil {
				return fmt.Errorf("relay Codex daemon to stdout: %w", err)
			}
			return nil
		}
	}
}
