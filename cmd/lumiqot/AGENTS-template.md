# Lumiqot environment
{{ if .GitHubSignedIn }}
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
