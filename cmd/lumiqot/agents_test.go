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

	status := statusHistory{
		SchemaVersion: 1,
		statusSnapshot: statusSnapshot{Components: map[string]json.RawMessage{
			"auth":    json.RawMessage(`{"state":"available","value":{"gh":"signed_in"}}`),
			"gateway": json.RawMessage(`{"state":"available","value":{"config":{"listen":"[::]:8443"}}}`),
		}},
		ChangedComponents: []string{"gateway"},
	}
	if err := updateAgents(home, status); err != nil {
		t.Fatal(err)
	}
	if got := read(generated); !strings.Contains(got, "`gh` CLI is signed in") {
		t.Fatalf("unexpected instructions: %q", got)
	}

	write(templatePath, "{{ .schemaVersion }} {{ index .changedComponents 0 }} {{ .components.gateway.value.config.listen }}")
	if err := updateAgents(home, status); err != nil {
		t.Fatal(err)
	}
	if got := read(generated); got != "1 gateway [::]:8443" {
		t.Fatalf("status JSON was not available to the template: %q", got)
	}

	write(notice, "Custom notice")
	write(templatePath, "Custom instructions")
	if err := installAgentsFiles(home); err != nil {
		t.Fatal(err)
	}
	if read(templatePath) != "Custom instructions" || read(notice) != "Custom notice" {
		t.Fatal("package activation overwrote an existing file")
	}
	if err := updateAgents(home, status); err != nil || read(generated) != "Custom instructions" {
		t.Fatalf("custom template was not applied: %v", err)
	}
	write(templatePath, "{{")
	if err := updateAgents(home, status); err == nil || read(generated) != "Custom instructions" {
		t.Fatalf("invalid template did not preserve existing instructions: %v", err)
	}
}

func TestAgentsGitHubAccountInstructions(t *testing.T) {
	home := t.TempDir()
	for _, test := range []struct {
		accountType string
		label       string
	}{
		{"personal", "personal account"},
		{"work", "work account"},
		{"ai", "AI-dedicated account"},
	} {
		t.Run(test.accountType, func(t *testing.T) {
			status := statusHistory{SchemaVersion: 1, statusSnapshot: statusSnapshot{
				Components: map[string]json.RawMessage{
					"githubCli": json.RawMessage(`{"state":"available","value":{"login":"fixture-user","accountType":"` + test.accountType + `"}}`),
				},
			}}
			if err := updateAgents(home, status); err != nil {
				t.Fatal(err)
			}
			output, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(output), "`fixture-user` ("+test.label+")") {
				t.Fatalf("unexpected account instructions: %q", output)
			}
		})
	}
}

func TestAgentsTemplateAutoUpdate(t *testing.T) {
	for _, test := range []struct {
		name string
		old  string
		want string
	}{
		{"managed", agentsUpdateMarker + " previous release */ -}}\nOld instructions\n", string(defaultAgentsTemplate)},
		{"marker removed", "# Custom instructions\n", "# Custom instructions\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			path := agentsTemplatePath(home)
			if err := writeAgentsFile(path, []byte(test.old), false); err != nil {
				t.Fatal(err)
			}
			if err := updateAgents(home, statusHistory{SchemaVersion: 1}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != test.want {
				t.Fatalf("template = %q (%v), want %q", data, err, test.want)
			}
			output, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
			if err != nil || strings.Contains(string(output), "LUMIQOT-AUTO-UPDATE") {
				t.Fatalf("generated instructions = %q (%v)", output, err)
			}
		})
	}
}
