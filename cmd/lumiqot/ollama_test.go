package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOllamaInstalledModelsWithoutInference(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/version":
			fmt.Fprint(w, `{"version":"test-version"}`)
		case "/api/tags":
			fmt.Fprint(w, `{"models":[]}`)
		case "/api/ps":
			fmt.Fprint(w, `{"models":[]}`)
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	report := inspectOllama(context.Background(), server.Client(), server.URL)
	if !report.Installed || !report.Running || report.Version != "test-version" || report.Models == nil || len(report.Models) != 0 || report.Diagnostic != "" {
		t.Fatalf("bad report: %+v", report)
	}
	if strings.Join(requests, ",") != "GET /api/version,GET /api/tags,GET /api/ps" {
		t.Fatal(requests)
	}
}

func TestOllamaPullStreamingAndErrors(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		failed     bool
	}{
		{"progress", "{\"status\":\"pulling layer\",\"total\":100,\"completed\":20}\n{\"status\":\"success\"}\n", 200, false},
		{"registry error", "{\"error\":\"model does not exist\"}\n", 200, true},
		{"interrupted", "{\"status\":\"pulling manifest\"}\n", 200, true},
		{"bad data", "not JSON\n", 200, true},
		{"http error", "unavailable", 503, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/pull" {
					t.Error(r.Method, r.URL.Path)
				}
				var input struct {
					Model  string
					Stream bool
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != "fixture:small" || !input.Stream {
					t.Errorf("invalid pull: %+v, %v", input, err)
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			var status string
			var sawProgress bool
			err := pullOllama(context.Background(), server.Client(), server.URL, "fixture:small", func(s string, completed, total int64) {
				status = s
				if completed == 20 && total == 100 {
					sawProgress = true
				}
			})
			if (err != nil) != test.failed {
				t.Fatal(err)
			}
			if !test.failed && (status != "success" || !sawProgress) {
				t.Fatal(status, sawProgress)
			}
		})
	}
}

func TestOllamaPullCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := pullOllama(ctx, server.Client(), server.URL, "fixture", func(string, int64, int64) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestOllamaModelNameValidation(t *testing.T) {
	for _, model := range []string{"qwen:small", "library/model:q4_K_M", "model", "a-b.c_2:latest"} {
		if !validOllamaModel(model) {
			t.Error("rejected", model)
		}
	}
	for _, model := range []string{"", "-x", "../model", "model;touch x", "model\n", "https://example.com/model", "model:tag:other", strings.Repeat("a", 201)} {
		if validOllamaModel(model) {
			t.Error("accepted", model)
		}
	}
}

func TestOllamaPrivateOperationState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := ollamaStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeOllamaJob(dir, ollamaJob{Action: "pull", Model: "fixture:small", State: "running", Status: "pulling layer", Total: 100, Completed: 20}); err != nil {
		t.Fatal(err)
	}
	job := readOllamaJob(dir)
	if job == nil || job.Model != "fixture:small" || job.Completed != 20 || job.UpdatedAt.IsZero() {
		t.Fatalf("bad persisted job: %+v", job)
	}
}

func TestOllamaBackgroundSubmissionAndCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	commandDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(commandDir, 0700); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "submitted")
	t.Setenv("OLLAMA_TEST_TRACE", trace)
	t.Setenv("PATH", commandDir+":"+os.Getenv("PATH"))
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OLLAMA_TEST_TRACE\"\n"
	if err := os.WriteFile(filepath.Join(commandDir, "systemd-run"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// No worker is started by the fixture; it merely reports the submitted unit.
	systemctlPath := filepath.Join(commandDir, "systemctl")
	if err := os.WriteFile(systemctlPath, []byte("#!/bin/sh\nprintf 'LoadState=loaded\\nActiveState=active\\nUnitFileState=static\\nMainPID=0\\nControlGroup=/fixture\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	previous := userSystemctl
	userSystemctl = systemctlPath
	t.Cleanup(func() { userSystemctl = previous })
	if err := ollamaCommand([]string{"install"}); err != nil {
		t.Fatal(err)
	}
	submitted, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(submitted), "--unit="+ollamaJobUnit) || !strings.HasSuffix(string(submitted), "ollama\n_run\ninstall\n") {
		t.Fatalf("wrong submission: %s", submitted)
	}
	if err := ollamaCommand([]string{"install"}); err == nil {
		t.Fatal("accepted a second operation while busy")
	}
	if err := ollamaCommand([]string{"cancel"}); err != nil {
		t.Fatal(err)
	}
	stateDir, _ := ollamaStateDir()
	if job := readOllamaJob(stateDir); job == nil || job.State != "cancelled" {
		t.Fatalf("bad cancelled state: %+v", job)
	}
}
