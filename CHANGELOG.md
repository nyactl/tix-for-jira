# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- MCP server with read tools and approval-gated write tools (comment, field update, transition, create, work log, link).
- CLI with the same operations; writes are confirmed in the terminal.
- Privacy mode `own`: only your tickets, other people as stable placeholders.
- API token storage in the macOS Keychain with expiry warnings; `auth login --with-token` reads the token from stdin.
- Markdown <-> Atlassian Document Format conversion with `@[label]` mentions.
- Profiles (`--profile`, `TIX_JIRA_PROFILE`) to switch between sites; development builds require an explicit profile.
