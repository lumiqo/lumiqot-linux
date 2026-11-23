package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsFilesAndStatusRendering(t *testing.T) {
	home := t.TempDir()
	if err := installAgentsFiles(home); err != nil {
		t.Fatal(err)
	}
	templatePath := agentsTemplatePath(home)
	notice := filepath.Join(home, ".codex", "AGENTS-Lumiqot-README.md")
	generated := filepath.Join(home, ".codex", "AGENTS.md")
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if read(templatePath) != string(defaultAgentsTemplate) || read(notice) != string(agentsReadme) {
		t.Fatal("package assets were not installed")
	}

	components := map[string]json.RawMessage{
		"auth":    json.RawMessage(`{"value":{"gh":"signed_in"}}`),
		"gateway": json.RawMessage(`{"value":{"config":{"listen":"[::]:8443","connectivity":{"provider":"local","public_url":"https://example.test"}}}}`),
	}
	if err := updateAgents(home, components); err != nil {
		t.Fatal(err)
	}
	if got := read(generated); !strings.Contains(got, "`gh` CLI is signed in") ||
		!strings.Contains(got, "embedded Caddy server on local port `8443`") {
		t.Fatalf("unexpected local Gateway instructions: %q", got)
	}

	components["auth"] = json.RawMessage(`{"value":{"gh":"signed_out"}}`)
	components["gateway"] = json.RawMessage(`{"value":{"config":{"listen":"127.0.0.1:8080","connectivity":{"provider":"managed-tunnel","public_url":"https://example.test"}}}}`)
	if err := updateAgents(home, components); err != nil {
		t.Fatal(err)
	}
	if got := read(generated); !strings.Contains(got, "Cloudflare Named Tunnel") ||
		strings.Contains(got, "Caddy") || strings.Contains(got, "8080") || strings.Contains(got, "`gh`") {
		t.Fatalf("unexpected tunnel instructions: %q", got)
	}

	write(notice, "Custom notice")
	write(templatePath, "Custom instructions")
	if err := installAgentsFiles(home); err != nil {
		t.Fatal(err)
	}
	if read(templatePath) != "Custom instructions" || read(notice) != "Custom notice" {
		t.Fatal("package activation overwrote an existing file")
	}
	if err := updateAgents(home, components); err != nil || read(generated) != "Custom instructions" {
		t.Fatalf("custom template was not applied: %v", err)
	}
	write(templatePath, "{{")
	if err := updateAgents(home, components); err == nil || read(generated) != "Custom instructions" {
		t.Fatalf("invalid template did not preserve existing instructions: %v", err)
	}
}
