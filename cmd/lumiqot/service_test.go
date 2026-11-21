package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServicePreferenceAndConvergence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	actual := "stopped"
	s := services[0]
	s.start = func(context.Context) error { actual = "running"; return nil }
	s.stop = func(context.Context) error { actual = "stopped"; return nil }
	s.status = func(context.Context) serviceStatus {
		if actual == "running" {
			return serviceStatus{Actual: actual, Health: "healthy", BootEnabled: true}
		}
		return serviceStatus{Actual: actual, Health: "not_applicable"}
	}
	ctx := context.Background()
	check := func(wantDesired, wantActual, wantHealth string, converged bool) {
		t.Helper()
		report := s.report(ctx)
		if report.Desired != wantDesired || report.Actual != wantActual || report.Health != wantHealth || report.Converged != converged {
			t.Fatalf("unexpected service report: %+v", report)
		}
	}
	check("disabled", "stopped", "not_applicable", true)
	if err := serviceCommand([]string{"check-enabled", "worker"}); err == nil {
		t.Fatal("systemd boot guard allowed a fresh Worker")
	}
	for _, enabled := range []bool{true, true, false, false} {
		if err := s.setEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := s.converge(ctx); err != nil {
				t.Fatal(err)
			}
		}
		bootErr := serviceCommand([]string{"check-enabled", "worker"})
		if (bootErr == nil) != enabled {
			t.Fatalf("unit boot guard disagrees with preference: %v", bootErr)
		}
		if enabled {
			check("enabled", "running", "healthy", true)
			if err := s.stop(ctx); err != nil {
				t.Fatal(err)
			}
			check("enabled", "stopped", "not_applicable", false)
		} else {
			check("disabled", "stopped", "not_applicable", true)
		}
	}
	path, err := preferencePath(s)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("preference permissions: %s", info.Mode())
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := serviceCommand([]string{"check-enabled", "worker"}); err == nil {
		t.Fatal("systemd boot guard accepted an invalid preference")
	}
	check("unknown", "stopped", "not_applicable", false)
}

func TestMixedServicePreferencesAndHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	worker := services[0]
	worker.status = func(context.Context) serviceStatus {
		return serviceStatus{Actual: "stopped", Health: "not_applicable"}
	}
	another := managedService{name: "example", unit: "lumiqot-example.service", defaultEnabled: true,
		status: func(context.Context) serviceStatus {
			return serviceStatus{Actual: "running", Health: "healthy", BootEnabled: true}
		}}
	managed := []managedService{worker, another}
	ctx := context.Background()
	if report := aggregateStatus(ctx, managed); report.State != "healthy" || !report.Ready || !report.Converged || len(report.Services) != 2 {
		t.Fatalf("mixed converged state: %+v", report)
	}
	if err := another.setEnabled(false); err != nil {
		t.Fatal(err)
	}
	if report := aggregateStatus(ctx, managed); report.State != "partial" || report.Ready || report.Converged ||
		report.Services[workerUnit].Health != "not_applicable" || report.Services[another.unit].Health != "healthy" {
		t.Fatalf("mixed process health and intent: %+v", report)
	}
}

func TestPreferenceRequestWaitsForPackageTransaction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), ".update.lock")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	s := services[0]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transaction, err := lockLifecycle(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	busy, cancelBusy := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelBusy()
	if lock, err := lockLifecycle(busy, path); !errors.Is(err, context.DeadlineExceeded) {
		if lock != nil {
			lock.Close()
		}
		t.Fatalf("concurrent request did not honor the transaction: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		lock, err := lockLifecycle(ctx, path)
		if err == nil {
			defer lock.Close()
			err = s.setEnabled(true)
		}
		done <- err
	}()
	if enabled, err := s.enabled(); enabled || err != nil {
		t.Fatalf("intent changed during transaction: %t, %v", enabled, err)
	}
	if err := transaction.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if enabled, err := s.enabled(); !enabled || err != nil {
		t.Fatalf("queued preference failed to persist: %t, %v", enabled, err)
	}
}
