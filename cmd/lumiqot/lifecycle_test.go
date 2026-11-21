package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerStopWaitsForDrain(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	commands := filepath.Join(runtimeDir, "commands")
	t.Setenv("LUMIQOT_TEST_COMMANDS", commands)
	systemctl := filepath.Join(runtimeDir, "systemctl")
	const script = `#!/bin/sh
case "$2" in
show)
    active=active
    if [ -e "$LUMIQOT_TEST_COMMANDS" ]; then active=inactive; fi
    printf '%s\n' 'LoadState=loaded' "ActiveState=$active" 'MainPID=0'
    ;;
stop|disable) printf '%s\n' "$2" >> "$LUMIQOT_TEST_COMMANDS" ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(systemctl, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := userSystemctl
	userSystemctl = systemctl
	t.Cleanup(func() { userSystemctl = old })

	dir := filepath.Join(runtimeDir, "lumiqot")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var draining atomic.Bool
	var active atomic.Int32
	active.Store(1)
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/drain" {
			draining.Store(true)
		}
		_ = json.NewEncoder(w).Encode(workerControl{Ready: true, Draining: draining.Load(), Active: int(active.Load())})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	err = stopWorker(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || !draining.Load() {
		t.Fatalf("expected an armed drain timeout: %v", err)
	}
	if _, err := os.Stat(commands); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed-out drain issued a systemd mutation: %v", err)
	}

	active.Store(0)
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := stopWorker(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(commands)
	if err != nil || !reflect.DeepEqual(strings.Fields(string(data)), []string{"stop"}) {
		t.Fatalf("unexpected stop sequence: %q %v", data, err)
	}
}
