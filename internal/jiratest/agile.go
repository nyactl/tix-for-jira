package jiratest

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type Board struct {
	ID            int
	Name, Type    string
	Project       string
	LocationOwner string // account ID for boards located in a user
}

type Sprint struct {
	ID, Board        int
	Name, State      string
	Start, End, Goal string
}

var (
	reBoard        = regexp.MustCompile(`^/rest/agile/1.0/board/([0-9]+)(/sprint|/backlog)?$`)
	reSprintIssues = regexp.MustCompile(`^/rest/agile/1.0/sprint/([0-9]+)/issue$`)
)

// agile serves the Jira Software endpoints; it reports false for other paths.
func (f *Fake) agile(w http.ResponseWriter, r *http.Request, me *User) bool {
	p, q := r.URL.Path, r.URL.Query()
	switch {
	case p == "/rest/agile/1.0/board":
		var out []any
		for _, b := range f.Boards {
			if pk := q.Get("projectKeyOrId"); pk == "" || pk == b.Project {
				out = append(out, f.boardJSON(b))
			}
		}
		writeJSON(w, 200, paged(r, "values", out))
	case reBoard.MatchString(p):
		m := reBoard.FindStringSubmatch(p)
		id, _ := strconv.Atoi(m[1])
		b := f.board(id)
		if b == nil {
			errJSON(w, 404, "Board does not exist or you do not have permission to see it.")
			return true
		}
		switch m[2] {
		case "":
			writeJSON(w, 200, f.boardJSON(*b))
		case "/sprint":
			if b.Type != "scrum" {
				errJSON(w, 400, "The board does not support sprints")
				return true
			}
			states := q.Get("state")
			var out []any
			for _, s := range f.Sprints {
				if s.Board == id && (states == "" || strings.Contains(states, s.State)) {
					out = append(out, map[string]any{"id": s.ID, "name": s.Name, "state": s.State, "startDate": s.Start, "endDate": s.End, "goal": s.Goal})
				}
			}
			writeJSON(w, 200, paged(r, "values", out))
		case "/backlog":
			f.agileIssues(w, r, me, func(i *Issue) bool {
				return i.Project == b.Project && i.Sprint == 0 && statuses[i.Status].cat != "done"
			})
		}
	case reSprintIssues.MatchString(p):
		id, _ := strconv.Atoi(reSprintIssues.FindStringSubmatch(p)[1])
		f.agileIssues(w, r, me, func(i *Issue) bool { return i.Sprint == id })
	default:
		return false
	}
	return true
}

func (f *Fake) board(id int) *Board {
	for i := range f.Boards {
		if f.Boards[i].ID == id {
			return &f.Boards[i]
		}
	}
	return nil
}

func (f *Fake) boardJSON(b Board) map[string]any {
	loc := map[string]any{"projectKey": b.Project}
	if b.LocationOwner != "" {
		u := f.Users[b.LocationOwner]
		loc = map[string]any{"userId": 1, "userAccountId": u.AccountID, "displayName": u.DisplayName, "name": u.DisplayName}
	}
	return map[string]any{"id": b.ID, "name": b.Name, "type": b.Type, "location": loc}
}

func (f *Fake) agileIssues(w http.ResponseWriter, r *http.Request, me *User, keep func(*Issue) bool) {
	jql := r.URL.Query().Get("jql")
	if jql != "" {
		f.JQL = append(f.JQL, jql)
	}
	var out []any
	for _, i := range f.sortedIssues() {
		if !keep(i) {
			continue
		}
		if !f.UnscopedSearch && strings.Contains(jql, "assignee = currentUser()") && i.Assignee != me.AccountID {
			continue
		}
		out = append(out, f.issueJSON(i, false, me))
	}
	writeJSON(w, 200, paged(r, "issues", out))
}
