# Lumiqot-generated Codex instructions

Lumiqot automatically updates `~/.codex/AGENTS.md` when it refreshes the
environment status. Before customizing `~/.config/lumiqot/AGENTS-template.md`,
remove the first line containing `LUMIQOT-AUTO-UPDATE` to keep your changes.
Lumiqot renders the template during the next status refresh.
The template receives the decoded status JSON, with fields such as
`.schemaVersion`, `.collectedAt`, and `.components.githubCli.value`.
