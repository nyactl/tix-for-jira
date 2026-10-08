# Security policy

## Supported versions

Only the latest release receives fixes.

## Reporting a vulnerability

Please do not open a public issue. Report privately through the repository's **Security** tab → **Report a vulnerability**, with the version or commit, steps to reproduce and the impact you expect.

You will get an acknowledgement within 7 days. Fixes are released before details are published, and reporters are credited unless they prefer otherwise.

## What tix-jira promises

Reports that break any of these are in scope:

1. **Scope:** in privacy mode `own`, no ticket that is not assigned to the user reaches the output, beyond the key, type and status of related tickets.
2. **Redaction:** in mode `own`, other people's account IDs, emails and display names from structured data never reach the output; display names and emails of people seen in structured data are also replaced in free text.
3. **Consent:** no write happens without an approval given by the human, in the terminal (CLI) or in an elicitation dialog (MCP). Approvals are single-use, expire after 10 minutes and are bound to the exact change shown.
4. **Containment:** the API token is only sent to the configured `https` site; redirects to another host or scheme are refused; the token is stored only in the macOS Keychain.
5. **Output safety:** terminal control sequences, bidirectional overrides and invisible characters from Jira are removed before output.

Known limits, not vulnerabilities: names typed in free text by people who never appear in structured data; the contents of attachments; a program that controls a pseudo-terminal can type a CLI confirmation; MCP clients configured to auto-answer elicitations.
