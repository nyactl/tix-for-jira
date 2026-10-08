# Contributing

Thanks for helping improve tix-jira. Bug reports, fixes and focused feature proposals are welcome.

## Before you start

- For anything bigger than a small fix, open an issue first to agree on the approach.
- Security problems go through private reporting, see [SECURITY.md](SECURITY.md).
- By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development

Requires macOS (for the Keychain code) and Go 1.26 or newer.

```sh
git clone git@github.com:nyactl/tix-jira.git
cd tix-jira
go test -race ./...
golangci-lint run ./...
```

Unit tests must not contact a real Jira site: use `internal/jiratest`, the in-memory fake. Changes to Jira API usage should also be checked with the integration suite, see [integration/README.md](integration/README.md).

## Rules that keep tix-jira safe

Read [docs/design.md](docs/design.md) first. In short:

- Only `internal/core` touches raw Jira data. It returns views with people already replaced; never print or return `jira` types directly.
- Every read of ticket data goes through the scope check (`scoped`, `InScope`), and people in structured data are registered (`learn`) before free text is redacted.
- Every write is a `core.Plan` whose description states exactly what will change, confirmed through the terminal (CLI) or the approval gate (MCP).
- New MCP tools need correct `readOnlyHint` / `destructiveHint` annotations; write tools are registered only with `--allow-writes`.
- Build strings with invisible characters in tests with `string(rune(...))`; a test rejects them in source files.

## Commits and pull requests

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/), e.g. `fix(core): keep labels stable across searches`; mark breaking changes with `!`.
- Keep pull requests focused; tests, lint and govulncheck must pass.
- Add a line to the "Unreleased" section of [CHANGELOG.md](CHANGELOG.md) for user-visible changes.
