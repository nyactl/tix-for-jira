# tix-for-jira

`tix` lets you work on **your own** Jira Cloud tickets from the terminal or through an MCP client, without exposing your colleagues or letting anything change Jira behind your back.

- **Privacy scope:** by default only tickets assigned to you are visible. Other people appear as "Person A", "Person B", …, never by name, email or account ID.
- **Every change needs your approval:** in the terminal you confirm each write; over MCP each write opens an approval dialog that the assistant cannot answer.
- **Token in the Keychain:** your API token is stored in the macOS Keychain, readable only by tix-for-jira without a prompt.
- **No admin rights needed:** everything runs with your own Jira permissions.

> Not affiliated with or endorsed by Atlassian. Jira is a trademark of Atlassian.

## Install

Requires macOS and Go 1.26+. The repository is private, so Go fetches it over SSH:

```sh
go env -w GOPRIVATE=github.com/nyactl/*
git config --global url."git@github.com:nyactl/".insteadOf "https://github.com/nyactl/"
go install github.com/nyactl/tix-for-jira/cmd/tix@latest
```

This installs the `tix` command. Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## Set up

1. Create an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>. Note its expiry date.
2. Log in:

   ```sh
   tix auth login
   ```

   tix-for-jira asks for your site (e.g. `your-team.atlassian.net`), email, token (hidden) and the token's expiry date, verifies them and stores the token in the Keychain. It warns you two weeks before the token expires. To pass the token from a password manager instead, pipe it in with `--with-token` (together with `--site`, `--email` and `--expires`).

3. Check: `tix auth status`

If your organisation has disabled personal API tokens, tix-for-jira cannot connect.

## Use from the terminal

```sh
tix mine                         # your open tickets, most recently updated first
tix mine --since -1d             # ... changed in the last day
tix show PROJ-123                # one ticket with description, links, attachments
tix comments PROJ-123
tix history PROJ-123 --since -7d
tix search 'project = PROJ AND priority = High'

tix comment PROJ-123 --body "Fixed in **v2.1**, thanks @[Person A]"
tix update PROJ-123 --set "Labels=auth,urgent" --set "Story Points=3"
tix transition PROJ-123 "In Progress"
tix log PROJ-123 1h30m --started 09:00 --comment "Code review"
tix create --project PROJ --type Task --summary "Follow-up" --description -   # description from stdin
tix link PROJ-123 "is blocked by" PROJ-124
```

Add `--json` to any read command for machine-readable output. `tix help <command>` explains every option.

**Confirmation:** each write shows exactly what will change. Additive changes (comment, create, log work, link) ask `y/N`; destructive ones (field updates, transitions) ask you to type the ticket key. The answer is read from the terminal device, not from stdin, so scripts, pipes and assistants running the command cannot confirm for you. There is no `--yes`.

## Use with an MCP client

Register tix-for-jira as a stdio MCP server in your client's configuration, for example:

```json
{
  "mcpServers": {
    "tix-for-jira": {
      "command": "tix",
      "args": ["mcp", "--allow-writes"]
    }
  }
}
```

No credentials go into the client configuration; tix-for-jira reads them from its own config and the Keychain.

- Without `--allow-writes` the server only offers read tools.
- With it, write tools are offered, but **each call opens an approval dialog** (MCP elicitation) showing the exact change. Only you can answer it. Clients without elicitation support cannot write.
- Do not configure auto-approval or elicitation hooks for this server; they would answer on your behalf.

| Read tools | Write tools (with `--allow-writes`) |
|---|---|
| `whoami`, `my_issues`, `search_issues`, `get_issue`, `list_comments`, `get_history`, `list_worklogs`, `list_transitions`, `list_editable_fields`, `get_attachment`, `list_link_types`, `list_projects`, `list_issue_types` | `add_comment`, `update_fields`, `transition_issue`, `create_issue`, `log_work`, `link_issues` |

Every tool carries `readOnlyHint` / `destructiveHint` annotations.

## Profiles: test and production

Each profile has its own site, account, privacy mode and token, so a test site and your production site never mix:

```sh
tix auth login                      # profile "default", e.g. production
tix --profile test auth login       # a test site
tix config profiles                 # list profiles; * marks the selected one
tix --profile test mine             # or: export TIX_JIRA_PROFILE=test
```

Every confirmation prompt and approval dialog starts with the site and profile it applies to. For MCP, register one server per profile, e.g. `tix --profile test mcp`.

### Developing while using it

Keep a released build for daily work and a development build for the test site:

```sh
go install github.com/nyactl/tix-for-jira/cmd/tix@latest   # released build: tix, profile "default"
make dev                                                    # development build: tix-dev
tix-dev --profile test mine
```

Development builds refuse to run unless a profile is chosen explicitly (`--profile` or `TIX_JIRA_PROFILE`), so work in progress cannot reach production by accident. macOS asks for Keychain access again after each rebuild, because the binary changed.

## Privacy modes

| | `own` (default) | `off` |
|---|---|---|
| Tickets | only those assigned to you; linked or parent tickets show key, type and status | everything your account can see |
| People | "Me", "Person A", "Person B", … | real names |

Switch with `tix config privacy off|own`. Switching to `off` must be confirmed in the terminal; restart MCP clients afterwards.

Names that someone typed into free text are replaced only if that person also appears in a structured field (assignee, reporter, author, mention). Attachments are not redacted. See [docs/design.md](docs/design.md) for the details.

## Limitations

- Jira Cloud only; macOS only (Keychain).
- People fields (assignee, reviewers) cannot be changed; tickets you create are assigned to you.
- No deleting of tickets, comments or worklogs, and no uploads.
- Durations are hours and minutes; days and weeks depend on site settings and are rejected.

## Development

```sh
go test -race ./...
golangci-lint run ./...
```

Tests use an in-memory fake of Jira Cloud. Tests against a real test site: [integration/README.md](integration/README.md). Design and security model: [docs/design.md](docs/design.md).

Contributions are welcome, see [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues privately, see [SECURITY.md](SECURITY.md).

## License

MIT, see [LICENSE](LICENSE).
