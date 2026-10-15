# tix-for-jira

`tix` lets you work on **your own** Jira Cloud tickets from the terminal or through an MCP client, without exposing your colleagues or letting anything change Jira behind your back.

- **Privacy scope:** by default only tickets assigned to you are visible. Other people appear as "Person A", "Person B", …, never by name, email or account ID.
- **Every change needs your approval:** in the terminal you confirm each write; over MCP each write opens an approval dialog that the assistant cannot answer.
- **Token in the system keychain:** your API token is stored in the macOS Keychain, readable only by tix-for-jira without a prompt, or on Linux in the Secret Service (GNOME Keyring, KWallet). Never in a file or environment variable.
- **No admin rights needed:** everything runs with your own Jira permissions.

For Confluence pages there is a companion tool, [dok-for-confluence](https://github.com/nyactl/dok-for-confluence), with the same approval and privacy model.

![tix lists only your tickets, shows a colleague as Person A, and asks before each change](docs/demo.gif)

> Not affiliated with or endorsed by Atlassian. Jira is a trademark of Atlassian.

## tix-for-jira or Atlassian's Rovo MCP server?

Atlassian offers its own hosted [Rovo MCP server](https://developer.atlassian.com/cloud/rovo-mcp/) for Jira, Confluence and other Atlassian apps. It is the broader tool; tix-for-jira is the narrower, stricter one. Both can be used side by side.

| | tix-for-jira | Rovo MCP server |
|---|---|---|
| What the assistant sees | tickets assigned to you (or, if you switch privacy off, what your account sees) | everything your account can see |
| Other people | "Person A", "Person B"; no names, emails or account IDs | as stored in Jira |
| Writes | each one approved by you in a dialog the assistant cannot answer, single-use and bound to the shown change | as your AI client handles tool calls |
| Runs | locally; the token stays in your system keychain | hosted by Atlassian, OAuth or API token |
| Covers | Jira | Jira, Confluence, Jira Service Management, Bitbucket, Loom and more, with semantic search |
| Cost | free, open source | some tools use Rovo credits |

Use tix-for-jira when colleagues' data must not reach the assistant, or when nothing should change in Jira without your explicit approval. Use Rovo for broad research across Atlassian apps where that is acceptable.

## Install

Runs on macOS and Linux. With Homebrew:

```sh
brew install nyactl/tap/tix-for-jira
```

Or with Go 1.26+:

```sh
go install github.com/nyactl/tix-for-jira/cmd/tix@latest
```

Both install the `tix` command. After each upgrade, macOS asks once whether the new binary may read the token from the Keychain.

On Linux, tix-for-jira needs a running Secret Service, which desktop sessions with GNOME Keyring or KWallet provide. Unlike the macOS Keychain, the Secret Service does not tie the token to tix-for-jira: any program in your unlocked session can read it. Release archives for Linux (amd64, arm64) are also attached to each [GitHub release](https://github.com/nyactl/tix-for-jira/releases).

Release archives are signed with cosign; see [RELEASING.md](RELEASING.md#verifying-release-signatures) to verify them.

## Set up

1. Create an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>, preferably a scoped one (see [Scoped API tokens](#scoped-api-tokens)). Note its expiry date.
2. Log in:

   ```sh
   tix auth login
   ```

   tix-for-jira asks for your site (e.g. `your-team.atlassian.net`), email, token (hidden) and the token's expiry date, verifies them and stores the token in the Keychain. It warns you two weeks before the token expires. To pass the token from a password manager instead, pipe it in with `--with-token` (together with `--site`, `--email` and `--expires`).

3. Check: `tix auth status`

If your organisation has disabled personal API tokens, tix-for-jira cannot connect.

### Scoped API tokens

A classic API token can do everything your account can do, in every Atlassian product. A scoped token ("Create API token with scopes", app **Jira**) is limited to the scopes you pick, so a leaked token exposes less, and a token without write scopes makes Jira itself refuse changes. `tix auth login` recognises a scoped token and sends all requests through Atlassian's API gateway (`api.atlassian.com`), which is the only place scoped tokens work.

| Use | Scopes |
|---|---|
| Read tickets | `read:jira-work`, `read:jira-user` |
| Also change tickets | add `write:jira-work` |
| Read boards and sprints | add `read:board-scope:jira-software`, `read:sprint:jira-software`, `read:issue-details:jira`, `read:project:jira`, `read:jql:jira`, `read:issue:jira-software` |
| Also move tickets between sprints and the backlog | add `write:sprint:jira-software`, `write:board-scope:jira-software` |

If a scope is missing, tix names the scopes the failed request needs.

## Use from the terminal

```sh
tix mine                         # your open tickets, most recently updated first
tix mine --since -1d             # ... changed in the last day
tix pick                         # browse with the arrow keys: show, open, comment; q quits
tix show PROJ-123                # one ticket with description, links, attachments
tix open PROJ-123                # in the browser (--print shows the URL)
tix comments PROJ-123
tix history PROJ-123 --since -7d
tix history PROJ-123 --status-times   # how long it spent in each status
tix search 'project = PROJ AND priority = High'
tix config search add urgent 'priority = Highest'   # then: tix search @urgent
tix config project PROJ          # default for tix create
tix sprint show "PROJ board"       # your tickets in the active sprint; also tix boards, tix backlog <board>

tix comment PROJ-123 --body "Fixed in **v2.1**, thanks @[Person A]"
tix update PROJ-123 --set "Labels=auth,urgent" --set "Story Points=3" --set "Original estimate=2h 30m"
tix transition PROJ-123 "In Progress"
tix log PROJ-123 1h30m --started 09:00 --comment "Code review"
tix create --project PROJ --type Task --summary "Follow-up" --description -   # description from stdin
tix link PROJ-123 "is blocked by" PROJ-124
tix take PROJ-130                  # assign an unassigned ticket to yourself
tix watch PROJ-123                 # and tix unwatch PROJ-123
tix weblink PROJ-123 https://ci.example.com/run/7 "CI run"
tix attach PROJ-123 ./screenshot.png   # upload a file (up to 50 MiB)
tix sprint move PROJ-123 "Sprint 12"   # or: tix sprint move PROJ-123 backlog
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
| `whoami`, `my_issues`, `search_issues`, `list_saved_searches`, `get_issue`, `list_comments`, `get_history`, `get_status_times`, `list_worklogs`, `list_transitions`, `list_editable_fields`, `get_attachment`, `list_link_types`, `list_projects`, `list_issue_types`, `list_boards`, `list_sprints`, `get_active_sprint`, `get_backlog` | `add_comment`, `update_fields`, `transition_issue`, `create_issue`, `log_work`, `link_issues`, `assign_to_me`, `attach_file`, `add_web_link`, `add_web_link`, `watch_issue`, `unwatch_issue`, `move_to_sprint` |

Prompts, offered by MCP clients as ready-made requests: `morning-overview` (what changed on my tickets since yesterday), `ticket-summary` (goal, state, open questions and next steps of one ticket) and `sprint-status` (my part of the active sprint). They only use the read tools above.

Read tools return their result twice: as Markdown for the model, and as structured content with an output schema for clients that process results. Both hold the same redacted and sanitized view.

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

Development builds refuse to run unless a profile is chosen explicitly (`--profile` or `TIX_JIRA_PROFILE`) and that profile has opted in with `tix config dev-builds on --profile test` (confirmed in the terminal). The default profile can never opt in, so work in progress cannot reach production by accident. macOS asks for Keychain access again after each rebuild, because the binary changed.

## Privacy modes

| | `own` (default) | `off` |
|---|---|---|
| Tickets | only those assigned to you; linked or parent tickets show key, type and status | everything your account can see |
| People | "Me", "Person A", "Person B", … | real names |

Switch with `tix config privacy off|own`. Switching to `off` must be confirmed in the terminal; restart MCP clients afterwards.

Names that someone typed into free text are replaced only if that person also appears in a structured field (assignee, reporter, author, mention). Attachments are not redacted. See [docs/design.md](docs/design.md) for the details.

## Limitations

- Jira Cloud only; macOS and Linux (no Windows).
- On Linux, a Secret Service is required; headless servers without one cannot store the token.
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
