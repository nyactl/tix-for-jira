# Integration tests

These tests run tix-jira against a real Jira Cloud site. They create tickets, comments, links, worklogs and an attachment, so **use a dedicated test site**, never a production one.

## One-time setup

1. Create a free Jira Cloud site at <https://www.atlassian.com/software/jira/free>. You become its admin.
2. Create a software project, e.g. key `TIX`, using the Kanban or Scrum template.
3. Invite a second account (a second email address you own). Give it normal product access only, **not** admin rights. The tests run as this account, which proves tix-jira works without admin permissions.
4. As the admin, create one ticket in the project and assign it to yourself (the admin). This is "someone else's ticket" for the tests.
5. Log the second account into a separate tix-jira profile, so your real setup stays untouched:

   ```sh
   tix-jira --profile test auth login     # use the second account and its API token
   ```
6. Optional, to check that other people are replaced by placeholders: let another account comment on one of the test user's tickets and @mention the test user.

## Running

```sh
export TIX_JIRA_PROFILE=test
export TIX_JIRA_IT_PROJECT=TIX       # project key from step 2
export TIX_JIRA_IT_OTHER=TIX-1       # ticket from step 4 (not assigned to the test user)
# optional, step 6:
export TIX_JIRA_IT_PEOPLE_ISSUE=TIX-7          # the test user's ticket with the other person's comment
export TIX_JIRA_IT_OTHER_NAME="Their Name"     # the other person's display name; must not appear in any output
go test -tags integration -count=1 -v ./integration/
```

The token is read from the Keychain item created by `tix-jira auth login`. Because the test binary is a different program, macOS asks once whether it may read that item; choose **Allow** (not "Always Allow", as the test binary changes on every build).

Tickets created by the tests carry the label `tix-jira-it` and a timestamp. tix-jira cannot delete tickets, so remove them in Jira when you no longer need them.

## What is checked

- authentication and all reads work for a non-admin account
- a ticket assigned to someone else is invisible to reads, searches and writes
- Markdown descriptions and comments survive the round trip through Jira's rich-text format
- field updates (including a number field and the due date where available), transitions with comments, subtasks, worklogs with start times, attachment downloads, and links, including that "A blocks B" is stored in the right direction
- with step 6: another person's name appears nowhere in issue, comment or history output; they show up as "Person A"
