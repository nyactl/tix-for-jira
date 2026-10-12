# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- `tix config dev-builds on|off`: development builds and the integration tests only use profiles that opted in; the default profile never can.
- Ticket details show fix versions, affects versions and the environment.
- Tickets can be moved under another parent or epic of your own (`--set Parent=PROJ-7`).
- Issue types match regardless of case, hyphens and spaces ("subtask" finds "Sub-task").
- Original and remaining estimates can be changed (`--set "Original estimate=2h 30m"`, MCP `update_fields`).
- More field types can be set: cascading selects (`--set "Region=Europe / Germany"`), URL fields (http and https only) and Team fields (by team name or ID). Atlassian Goals fields are not supported.

## [0.1.1] - 2026-10-09

### Added

- Homebrew formula: `brew install nyactl/tap/tix-for-jira`.
- Signed release archives for Apple Silicon and Intel Macs.

### Fixed

- `tix auth login` checks the token right after it is entered and says clearly when Jira rejects it; nothing is saved then.

## [0.1.0] - 2026-10-09

First release, tested against Jira Cloud with a non-admin account.

### Added

- MCP server with read tools and approval-gated write tools (comment, field update, transition, create, work log, link).
- CLI with the same operations; writes are confirmed in the terminal.
- Privacy mode `own`: only your tickets, other people as stable placeholders.
- API token storage in the macOS Keychain with expiry warnings; `auth login --with-token` reads the token from stdin.
- Markdown <-> Atlassian Document Format conversion with `@[label]` mentions.
- Profiles (`--profile`, `TIX_JIRA_PROFILE`) to switch between sites; development builds require an explicit profile.

[0.1.1]: https://github.com/nyactl/tix-for-jira/releases/tag/v0.1.1
[0.1.0]: https://github.com/nyactl/tix-for-jira/releases/tag/v0.1.0
