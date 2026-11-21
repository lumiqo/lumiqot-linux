package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mockCodexSocket(t *testing.T) (context.Context, *net.UnixListener) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	deadline, _ := ctx.Deadline()
	listener.SetDeadline(deadline)
	return ctx, listener
}

func TestCodexBridgeDuplex(t *testing.T) {
	ctx, listener := mockCodexSocket(t)
	input, writeInput := io.Pipe()
	output, writeOutput := io.Pipe()
	defer input.Close()
	defer writeInput.Close()
	defer output.Close()
	defer writeOutput.Close()

	done := make(chan error, 1)
	go func() {
		defer writeOutput.Close()
		done <- bridgeCodexSocket(ctx, listener.Addr().String(), input, writeOutput)
	}()
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	deadline, _ := ctx.Deadline()
	peer.SetDeadline(deadline)

	fromClient := bytes.Repeat([]byte{0, 0xff, '\n', '\r', 0x80}, 64000)
	fromDaemon := bytes.Repeat([]byte{0xff, 0xfe, 0, '\r', '\n'}, 64000)
	// Write in both directions before either side closes its write half.
	writes := make(chan error, 2)
	go func() { _, err := writeInput.Write(fromClient); writes <- err }()
	go func() { _, err := peer.Write(fromDaemon); writes <- err }()
	readBytes(t, peer, fromClient)
	readBytes(t, output, fromDaemon)
	for range 2 {
		if err := <-writes; err != nil {
			t.Fatal(err)
		}
	}

	writeInput.Close()
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after stdin closed: %v, want EOF", err)
	}
	tail := []byte("reply after stdin closed")
	go func() { _, err := peer.Write(tail); writes <- err }()
	readBytes(t, output, tail)
	if err := <-writes; err != nil {
		t.Fatal(err)
	}
	peer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("relay changed the byte stream")
	}
}

func TestCodexBridgeDisconnect(t *testing.T) {
	ctx, listener := mockCodexSocket(t)
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- bridgeCodexSocket(ctx, listener.Addr().String(), input, io.Discard)
	}()
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	peer.Close()
	if err := <-done; err != nil {
		t.Fatalf("disconnect with stdin still open: %v", err)
	}
	listener.Close()
	if err := bridgeCodexSocket(ctx, listener.Addr().String(), input, io.Discard); err == nil {
		t.Fatal("expected a connection error")
	}
}

func TestCodexBridgeCancel(t *testing.T) {
	ctx, listener := mockCodexSocket(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- bridgeCodexSocket(ctx, listener.Addr().String(), input, io.Discard)
	}()
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	deadline, _ := ctx.Deadline()
	peer.SetDeadline(deadline)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled relay: %v", err)
	}
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after cancellation: %v, want EOF", err)
	}
}

func TestStartCodexDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	script := filepath.Join(dir, "codex")
	for _, tt := range []struct {
		name, body, path, errorText string
	}{
		{"existing daemon", `printf '{"status":"alreadyRunning","socketPath":"/tmp/app.sock"}\n'`, "/tmp/app.sock", ""},
		{"invalid JSON", `printf 'invalid\n'`, "", "decode Codex daemon start output"},
		{"missing path", `printf '{}\n'`, "", "did not return socketPath"},
		{"child failure", "exit 7", "", "exit status 7"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := "#!/bin/sh\n[ \"$*\" = 'app-server daemon start' ] || exit 99\necho diagnostic >&2\n" + tt.body + "\n"
			if err := os.WriteFile(script, []byte(source), 0700); err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			path, err := startCodexDaemon(context.Background(), &diagnostics)
			if path != tt.path || (tt.errorText == "" && err != nil) ||
				(tt.errorText != "" && (err == nil || !strings.Contains(err.Error(), tt.errorText))) {
				t.Fatalf("socket path = %q, error = %v", path, err)
			}
			if diagnostics.String() != "diagnostic\n" {
				t.Fatalf("stderr = %q", diagnostics.String())
			}
		})
	}
}
