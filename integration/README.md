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
6. For the two-person tests, log a second account of the same site into its own profile. The suite then acts as that person on its own: it creates a ticket it owns, comments on and hands over a ticket to the tested account, and checks that the tested account sees it only as "Person A":

   ```sh
   tix-jira --profile test2 auth login    # second account, its own API token
   ```

## Running

```sh
export TIX_JIRA_PROFILE=test
export TIX_JIRA_IT_PROJECT=TIX       # project key from step 2
export TIX_JIRA_IT_OTHER=TIX-1       # ticket from step 4 (not assigned to the test user)
export TIX_JIRA_IT_OTHER_PROFILE=test2         # optional, step 6
go test -tags integration -count=1 -v ./integration/
```

Tokens are read from the Keychain items created by `tix-jira auth login`. Because the test binary is a different program, macOS asks once per item whether it may read it; choose **Allow** (not "Always Allow", as the test binary changes on every build).

Tickets created by the tests carry the label `tix-jira-it` and a timestamp, and are moved to a done status when the run ends, so they no longer show up as open work. tix-jira cannot delete tickets; remove them in Jira when you no longer need them. To close tickets left open by interrupted runs, run the suite once with `TIX_JIRA_IT_CLEANUP=1`.

## What is checked

- authentication and all reads work for a non-admin account
- a ticket assigned to someone else is invisible to reads, searches and writes
- Markdown descriptions and comments survive the round trip through Jira's rich-text format
- field updates (including a number field and the due date where available), transitions with comments, subtasks, worklogs with start times, attachment downloads, and links, including that "A blocks B" is stored in the right direction
- with step 6: a ticket owned by another person is invisible; on a ticket they handed over, their name, email and account ID appear nowhere in issue, comment, link or history output, they show up as "Person A", and mentioning `@[Person A]` notifies the real person
