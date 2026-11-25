package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed AGENTS-template.md
var defaultAgentsTemplate []byte

//go:embed AGENTS-Lumiqot-README.md
var agentsReadme []byte

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

func updateAgents(home string, status statusHistory) error {
	path := agentsTemplatePath(home)
	data, err := syncAgentsTemplate(path)
	if err != nil {
		return fmt.Errorf("sync AGENTS template: %w", err)
	}
	source, err := template.New(filepath.Base(path)).Parse(string(data))
	if err != nil {
		return err
	}
	// Decode the snapshot as JSON so templates see the same field names and
	// value types as consumers of status.json.
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	var templateData map[string]any
	if err := json.Unmarshal(raw, &templateData); err != nil {
		return err
	}
	var output bytes.Buffer
	if err := source.Execute(&output, templateData); err != nil {
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
