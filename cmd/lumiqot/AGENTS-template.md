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
- See `~/.local/share/lumiqot/docs/ENVIRONMENT.md` for installed packages, tool versions, and environment-specific instructions.
{{- with .components }}
{{- if and .githubCli (eq .githubCli.state "available") .githubCli.value }}
{{- with .githubCli.value }}
- The `gh` CLI is signed in as `{{ .login }}` ({{ if eq .accountType "ai" }}AI-dedicated account{{ else }}{{ .accountType }} account{{ end }}).
{{- end }}
{{- else if and .auth (eq .auth.state "available") .auth.value (eq .auth.value.gh "signed_in") }}
- The `gh` CLI is signed in.
{{- end }}
{{- end }}

## Public service hosting

- Lumiqot Gateway provides infrastructure for exposing local services to the public internet.
- When publishing a service, use the existing Lumiqot Gateway infrastructure rather than creating a separate public entry point.
