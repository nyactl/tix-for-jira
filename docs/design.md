# tix-for-jira design

tix-for-jira lets an assistant work on **your own** Jira Cloud tickets through MCP, with a CLI for you. It is built around three rules:

1. **Scope:** by default the assistant only sees tickets assigned to you, and other people appear as stable placeholders ("Person A"), never by name, email or account ID.
2. **Consent:** every write needs explicit approval from the human. An assistant can request a write; it cannot approve it.
3. **Containment:** the API token lives in the macOS Keychain, readable only by the tix-for-jira binary without a prompt, or on Linux in the Secret Service of the user's session. Nothing the assistant controls can widen the scope, read the token, or skip a confirmation.

## Components

```
cmd: main.go
 └─ internal/cli         cobra commands, terminal confirmation
 └─ internal/mcpserver   MCP tools; approval through approvekit/approval
     └─ internal/core    operations: scope checks, redaction, write plans
         ├─ internal/jira      Jira endpoints on approvekit/atlassian
         ├─ internal/privacy   tix's scope rule (only my tickets)
         ├─ internal/render    views -> Markdown / JSON
         ├─ go-adf             Atlassian Document Format <-> Markdown
         └─ approvekit/people  labels for people, redaction
 internal/config    non-secret settings on approvekit/profile
 internal/jiratest  in-memory fake Jira Cloud for tests

 Shared modules: github.com/nyactl/go-adf, github.com/nyactl/approvekit
 (keychain, sanitize, approval, people, atlassian, profile).
```

`core` is the only layer that touches raw Jira data. It returns *views*: plain structs with Markdown text and person labels already applied. The MCP server and the CLI only ever render views, so no code path can accidentally print a raw API object with account IDs, emails or avatar URLs.

## Privacy scope

The mode is set in the config file and can only be changed from an interactive terminal (`tix config privacy`). The MCP server reads it once at start.

**`own` (default)**

- *Tickets:* a ticket is in scope when its assignee is the authenticated user. Searches are rewritten to `assignee = currentUser() AND (<your JQL>) ORDER BY …` and every result is checked again, so malformed JQL cannot widen the scope. Fetching a key that is out of scope returns the same "not found" as a missing ticket.
- *Related tickets:* parents, subtasks and linked tickets that are not in scope show only key, issue type and status.
- *Epics:* a ticket of someone else whose type is above standard tickets (an epic, hierarchy level 1 or more) may become the parent of one of your tickets, given by its key. tix then reads only its key, issue type and status, as for any parent; a key that is not such an epic gets the usual "not found". Lists of epics (`tix epics`, completion, the picker) come only from the parents of your own tickets, so they reveal nothing your tickets do not already show; there is no project-wide epic search and no MCP tool for one. The interactive picker alone also shows those epics' summaries, with known names replaced: it draws on the terminal device, which commands started by an assistant cannot open. Descriptions, comments, people and other children of such epics are never read. Subtasks still need a parent in scope.
- *People:* everyone other than you becomes "Person A", "Person B", … in order of first appearance. You are "Me". Labels are stable for the lifetime of the process. This applies to authors, assignees, reporters, mentions, change-history values for person fields, worklog and attachment authors, and person-type custom fields.
- *Free text:* the full display names and emails of people seen in structured data are replaced in descriptions and comments too. Names typed in free text that never appear in structured data cannot be detected; this is best-effort.
- *Attachments* are downloaded only for in-scope tickets. Their contents are not redacted.

**`off`** shows everything Jira shows you. Placeholders are not used.

Mentions in text written by the assistant use `@[Person A]` (or `@[Display Name]` in `off` mode). The label is resolved to the account ID inside tix-for-jira; only people already seen in this session can be mentioned, so the tool never queries the user directory.

## Writes and consent

Every write is built as a **plan** first: a human-readable description of exactly what will change, plus the function that applies it. The plan is shown to the human, and the apply step runs only after approval.

| Write | Kind |
|---|---|
| add comment, create issue, log work, link issues | additive |
| update fields, transition | destructive |

All writes need approval because even additive writes publish text under your name, which a prompt-injected assistant could misuse to leak data.

- **MCP:** write tools are registered only with `tix mcp --allow-writes`. Each call returns an elicitation request (SEP-2322 input request; the SDK falls back to a server-initiated elicitation on older protocol versions) showing the plan. The assistant cannot answer an elicitation. The request state is single-use, expires after 10 minutes and is HMAC-bound to the exact tool arguments. Clients without elicitation support cannot write.
- **CLI:** the confirmation is read from the controlling terminal, not stdin, so neither pipes nor an assistant running the command can answer it. Every write asks `y/N`, defaulting to no.

Deleting issues, comments or worklogs is not supported.

## Credentials

`tix auth login` asks for site, email and API token on the terminal (token without echo), verifies them against `/rest/api/3/myself`, and stores the token in the login Keychain. The item is created by tix-for-jira itself, so its access list trusts only the tix-for-jira binary; other programs, including `security find-generic-password`, trigger a macOS prompt. Rebuilding an unsigned binary changes its identity, so macOS asks once more after each rebuild. On Linux the token goes to the Secret Service (GNOME Keyring, KWallet) in the default collection; it is unlocked with the session and not bound to the binary. There is no fallback to a file or environment variable.

API tokens expire after at most a year. The expiry date can be recorded at login; tix-for-jira warns two weeks before it.

## Jira client

- Cloud REST v3 only; `https` required; Basic auth with email and token.
- Search uses `/rest/api/3/search/jql` with explicit `fields` and `nextPageToken` paging.
- Rich text is ADF. The [go-adf](https://github.com/nyactl/go-adf) module converts ADF to Markdown for reading and Markdown to schema-valid ADF for writing, with `@[label]` mentions through hooks. Known names are redacted in each ADF text piece before conversion and again in the Markdown.
- Attachments are fetched with `redirect=false`, so every request stays on the site host; redirects to another host or scheme are refused.
- Response bodies are size-limited; errors are mapped to clear messages (expired token, missing permission, feature disabled by the site admin).

No admin permissions are needed. Every call runs with your own permissions.

## Testing

- Unit tests per package, table-driven.
- `internal/jiratest` is an in-memory Jira Cloud fake with several users, so `core`, `mcpserver` and `cli` are tested end to end, including scope and redaction with other people's tickets.
- MCP tests use the SDK's in-memory transport with clients that accept, decline or lack elicitation.
- `go test -tags integration ./integration/...` runs against a real Jira Cloud test site with a non-admin user; see `integration/README.md`.
