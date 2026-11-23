{{/* LUMIQOT-AUTO-UPDATE: This template is updated automatically. Remove this line before editing to keep your changes. */ -}}
# Lumiqot environment
{{ if .GitHubCLI }}
- The `gh` CLI is signed in as `{{ .GitHubCLI.Login }}` ({{ if eq .GitHubCLI.AccountType "ai" }}AI-dedicated account{{ else }}{{ .GitHubCLI.AccountType }} account{{ end }}).
{{- if eq .GitHubCLI.AccountType "work" }}
- Do not commit, push, or perform similar Git operations unless the user has explicitly requested that specific operation.
{{- else if eq .GitHubCLI.AccountType "ai" }}
- Unless instructed otherwise, commit your changes as you go so your work is preserved.
{{- end }}
{{- else if .GitHubSignedIn }}
- The `gh` CLI is signed in.
{{- end }}
{{ if .Gateway }}
- Lumiqot Gateway has an entry point configured for the public internet.
{{- if .Gateway.LocalPort }}
- In Local Network mode, the Gateway uses its embedded Caddy server on local port `{{ .Gateway.LocalPort }}`.
{{- else }}
- The Gateway uses {{ .Gateway.Service }}.
{{- end }}
{{- end }}
