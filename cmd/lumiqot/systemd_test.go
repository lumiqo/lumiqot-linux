package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserSystemdBootCondition(t *testing.T) {
	if os.Getenv("LUMIQOT_TEST_SYSTEMD") != "1" {
		t.Skip("set LUMIQOT_TEST_SYSTEMD=1 in a running systemd user session")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		t.Fatal("XDG_RUNTIME_DIR must point to the running user session")
	}

	for _, test := range []struct {
		service  managedService
		unitFile []byte
	}{
		{services[0], workerUnitFile},
		{services[1], librarianUnitFile},
		{services[2], remoteUnitFile},
		{services[3], gatewayUnitFile},
	} {
		t.Run(test.service.name, func(t *testing.T) {
			checkUserSystemdBootCondition(t, runtime, test.service, test.unitFile)
		})
	}
}

func checkUserSystemdBootCondition(t *testing.T, runtime string, service managedService, unitFile []byte) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lumiqot")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	t.Setenv("HOME", dir)
	unitDir := filepath.Join(runtime, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0700); err != nil {
		t.Fatal(err)
	}
	unit, err := os.CreateTemp(unitDir, "lumiqot-boot-test-*.service")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(unit.Name())
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("systemctl %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() {
		run("disable", "--runtime", "--now", name)
		os.Remove(unit.Name())
		run("daemon-reload")
	})

	var condition string
	for _, line := range strings.Split(string(unitFile), "\n") {
		if strings.HasPrefix(line, "ExecCondition=") {
			condition = strings.Replace(line, packageRoot+"/current/bin/lumiqot", binary, 1)
			break
		}
	}
	if condition == "" {
		t.Fatal("bundled unit is missing the service preference guard")
	}
	stamp := filepath.Join(dir, "started")
	content := "[Service]\nType=oneshot\nRemainAfterExit=yes\nEnvironment=HOME=" + dir +
		"\n" + condition + "\nExecStart=/usr/bin/touch " + stamp + "\n[Install]\nWantedBy=default.target\n"
	if _, err := unit.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := unit.Close(); err != nil {
		t.Fatal(err)
	}

	run("daemon-reload")
	run("enable", "--runtime", name)
	checkStart := func(action string, wanted bool) {
		t.Helper()
		run(action, name)
		_, err := os.Stat(stamp)
		if wanted && err != nil || !wanted && !os.IsNotExist(err) {
			t.Fatalf("%s triggered ExecStart=%t, want %t (stat: %v)", action, err == nil, wanted, err)
		}
	}
	checkStart("start", service.defaultEnabled)
	if service.defaultEnabled {
		run("stop", name)
		if err := os.Remove(stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.setEnabled(true); err != nil {
		t.Fatal(err)
	}
	checkStart("start", true)
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	if err := service.setEnabled(false); err != nil {
		t.Fatal(err)
	}
	// Even with the boot enablement link left in place, systemd reevaluates
	// the persistent preference on start, including on a restart.
	run("is-enabled", name)
	checkStart("restart", false)
	run("daemon-reload")
	checkStart("start", false)
}
