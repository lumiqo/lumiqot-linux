{{/* LUMIQOT-AUTO-UPDATE: This template is updated automatically. Remove this line before editing to keep your changes. */ -}}

# Lumiqot

- This is an AI-dedicated Ubuntu environment running under WSL2.
- The `lumiqo` user account and its home directory are dedicated to AI agents.
- Passwordless sudo is available.
- You may install software and dependencies as needed without asking for permission.

## Available tooling

- Use `uv` for Python project dependencies.
- Use `mise` for language runtime management.
- `python-analysis` provides a preconfigured analysis environment.
- See `~/docs/ENVIRONMENT.md` for installed packages, tool versions, and environment-specific instructions.
{{- if .GitHubCLI }}
- The `gh` CLI is signed in as `{{ .GitHubCLI.Login }}` ({{ if eq .GitHubCLI.AccountType "ai" }}AI-dedicated account{{ else }}{{ .GitHubCLI.AccountType }} account{{ end }}).
{{- else if .GitHubSignedIn }}
- The `gh` CLI is signed in.
{{- end }}

## Public service hosting

- Lumiqot Gateway provides infrastructure for exposing local services to the public internet.
- When publishing a service, use the existing Lumiqot Gateway infrastructure rather than creating a separate public entry point.
