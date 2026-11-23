package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed AGENTS-template.md
var defaultAgentsTemplate []byte

//go:embed AGENTS-Lumiqot-README.md
var agentsReadme []byte

type agentsGateway struct {
	LocalPort string
	Service   string
}

type agentsTemplateData struct {
	GitHubSignedIn bool
	GitHubCLI      *githubCLIAccount
	Gateway        *agentsGateway
}

func agentsTemplatePath(home string) string {
	return filepath.Join(home, ".config", "lumiqot", "AGENTS-template.md")
}

const agentsUpdateMarker = "{{/* LUMIQOT-AUTO-UPDATE:"

func syncAgentsTemplate(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeAgentsFile(path, defaultAgentsTemplate, false); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, defaultAgentsTemplate) {
		return data, nil
	}

	firstLine, _, _ := bytes.Cut(data, []byte("\n"))
	if !bytes.HasPrefix(firstLine, []byte(agentsUpdateMarker)) {
		return data, nil
	}
	if err := writeAgentsFile(path, defaultAgentsTemplate, true); err != nil {
		return nil, err
	}
	return defaultAgentsTemplate, nil
}

func installAgentsFiles(home string) error {
	if _, err := syncAgentsTemplate(agentsTemplatePath(home)); err != nil {
		return fmt.Errorf("sync AGENTS template: %w", err)
	}
	path := filepath.Join(home, ".codex", "AGENTS-Lumiqot-README.md")
	if err := writeAgentsFile(path, agentsReadme, false); err != nil {
		return fmt.Errorf("install AGENTS notice: %w", err)
	}
	return nil
}

func updateAgents(home string, components map[string]json.RawMessage) error {
	path := agentsTemplatePath(home)
	data, err := syncAgentsTemplate(path)
	if err != nil {
		return fmt.Errorf("sync AGENTS template: %w", err)
	}
	source, err := template.New(filepath.Base(path)).Parse(string(data))
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := source.Execute(&output, agentsData(components)); err != nil {
		return err
	}
	path = filepath.Join(home, ".codex", "AGENTS.md")
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, output.Bytes()) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAgentsFile(path, output.Bytes(), true)
}

func agentsData(components map[string]json.RawMessage) agentsTemplateData {
	var auth struct {
		Value map[string]string
	}
	_ = json.Unmarshal(components["auth"], &auth)
	data := agentsTemplateData{GitHubSignedIn: auth.Value["gh"] == "signed_in"}
	var github struct {
		Value *githubCLIAccount
	}
	if err := json.Unmarshal(components["githubCli"], &github); err == nil && github.Value != nil &&
		githubLoginPattern.MatchString(github.Value.Login) && validGitHubAccountType(github.Value.AccountType) {
		data.GitHubCLI = github.Value
	}

	var gateway struct {
		Value struct {
			Config struct {
				Listen       string
				Connectivity struct {
					Provider  string
					PublicURL string `json:"public_url"`
				}
			}
		}
	}
	if err := json.Unmarshal(components["gateway"], &gateway); err != nil {
		return data
	}
	config := gateway.Value.Config
	if config.Connectivity.PublicURL == "" {
		return data
	}
	switch config.Connectivity.Provider {
	case "local":
		if _, port, err := net.SplitHostPort(config.Listen); err == nil && port != "" {
			data.Gateway = &agentsGateway{LocalPort: port}
		}
	case "managed-tunnel":
		data.Gateway = &agentsGateway{Service: "Cloudflare Named Tunnel"}
	case "home-relay":
		data.Gateway = &agentsGateway{Service: "Lumiqo Home Relay"}
	default:
		if config.Connectivity.Provider != "" {
			data.Gateway = &agentsGateway{Service: config.Connectivity.Provider}
		}
	}
	return data
}

func writeAgentsFile(path string, data []byte, replace bool) error {
	if !replace {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".agents-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if replace {
		return os.Rename(file.Name(), path)
	}
	if err := os.Link(file.Name(), path); !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
