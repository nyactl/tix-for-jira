# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- `tix open KEY` opens one of your tickets in the browser; `--print` shows the URL instead.
- `tix pick` lists your tickets in the terminal: choose one with the arrow keys, then show it, open it in the browser or comment on it (the comment is confirmed as usual).
- Time in status: `tix history KEY --status-times` and MCP `get_status_times` show how long a ticket spent in each status and how often it entered it.
- Saved searches per profile (`tix config search add NAME JQL`, `tix search @NAME`, MCP `search_issues` with `@NAME` and `list_saved_searches`), limited to the privacy scope like every search. A default project (`tix config project KEY`) for `tix create` and MCP `create_issue` without a project.
- MCP prompts `morning-overview`, `ticket-summary` and `sprint-status`, which use only the read tools.
- MCP read tools return structured content with output schemas alongside the Markdown, redacted and sanitized the same way.

## [0.2.0] - 2026-10-13

### Added

- `tix config dev-builds on|off`: development builds and the integration tests only use profiles that opted in; the default profile never can.
- Ticket details show fix versions, affects versions and the environment.
- Tickets can be moved under another parent or epic of your own (`--set Parent=PROJ-7`).
- Issue types match regardless of case, hyphens and spaces ("subtask" finds "Sub-task").
- Original and remaining estimates can be changed (`--set "Original estimate=2h 30m"`, MCP `update_fields`).
- More field types can be set: cascading selects (`--set "Region=Europe / Germany"`), URL fields (http and https only) and Team fields (by team name or ID). Atlassian Goals fields are not supported.
- `tix take KEY` and MCP `assign_to_me` assign an unassigned ticket to you. Tickets assigned to someone else are refused, and before the change the plan shows only the key, type and status.
- `tix watch` / `tix unwatch` and MCP `watch_issue` / `unwatch_issue`. Ticket details show the number of watchers and whether you are one of them, never who the others are.
- Boards and sprints: `tix boards`, `tix sprint list|show <board>`, `tix backlog <board>` and the MCP tools `list_boards`, `list_sprints`, `get_active_sprint`, `get_backlog`. In privacy mode `own` they show only your tickets plus the total number of tickets.
- `tix sprint move KEY SPRINT|backlog` and MCP `move_to_sprint` move one of your tickets into an active or future sprint (by ID or name) or back to the backlog.
- `tix attach KEY FILE` and MCP `attach_file` upload a local file of up to 50 MiB. The plan shows the file name, size and full path, and exactly the approved content is uploaded. The MCP tool only accepts absolute paths and refuses hidden files and folders.
- Web links: ticket details list them, and `tix weblink KEY URL [TITLE]` / MCP `add_web_link` add one (http and https only).
- Scoped API tokens: `tix auth login` recognises them and routes all requests through the Atlassian API gateway. A missing scope is named in the error. The README lists the scopes per use case.

### Changed

- Rich text is converted by the [go-adf](https://github.com/nyactl/go-adf) module. Comments and fields written as Markdown now always produce valid ADF (a heading in a list item or a nested quote no longer makes Jira reject the change), and text from Jira that looks like Markdown, such as `#`, `*` or `[x]`, is escaped so it is shown literally.
- Keychain storage, the MCP approval flow, labels for people, profiles and the HTTP client core now come from the [approvekit](https://github.com/nyactl/approvekit) module, shared with the Confluence tool [dok-for-confluence](https://github.com/nyactl/dok-for-confluence). Existing profiles and stored tokens keep working.

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

[0.2.0]: https://github.com/nyactl/tix-for-jira/releases/tag/v0.2.0
[0.1.1]: https://github.com/nyactl/tix-for-jira/releases/tag/v0.1.1
[0.1.0]: https://github.com/nyactl/tix-for-jira/releases/tag/v0.1.0
