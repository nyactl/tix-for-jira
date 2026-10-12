package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Board is a Jira Software board. A board located in a user's space names
// that person in its location.
type Board struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // scrum, kanban, simple
	Location struct {
		ProjectKey    string `json:"projectKey"`
		UserAccountID string `json:"userAccountId"`
		DisplayName   string `json:"displayName"`
	} `json:"location"`
}

type Sprint struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"` // future, active, closed
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Goal      string `json:"goal"`
}

func agileValues[T any](raw []byte) ([]T, int, bool, error) {
	var page struct {
		Values []T  `json:"values"`
		Total  int  `json:"total"`
		IsLast bool `json:"isLast"`
	}
	err := json.Unmarshal(raw, &page)
	return page.Values, page.Total, page.IsLast, err
}

// Boards lists the boards the user can see, optionally of one project.
func (c *Client) Boards(ctx context.Context, projectKey string, limit int) ([]Board, error) {
	q := url.Values{}
	if projectKey != "" {
		q.Set("projectKeyOrId", projectKey)
	}
	return startAtPages(ctx, c, "/rest/agile/1.0/board", q, limit, agileValues[Board])
}

func (c *Client) Board(ctx context.Context, id int) (*Board, error) {
	var b Board
	if err := c.do(ctx, http.MethodGet, "/rest/agile/1.0/board/"+strconv.Itoa(id), nil, nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// Sprints lists a board's sprints in the given states (e.g. "active,future").
func (c *Client) Sprints(ctx context.Context, boardID int, states string, limit int) ([]Sprint, error) {
	q := url.Values{}
	if states != "" {
		q.Set("state", states)
	}
	return startAtPages(ctx, c, "/rest/agile/1.0/board/"+strconv.Itoa(boardID)+"/sprint", q, limit, agileValues[Sprint])
}

// SprintIssues returns up to limit issues of a sprint matching jql, and the
// number of all issues in the sprint regardless of jql.
func (c *Client) SprintIssues(ctx context.Context, sprintID int, jql string, fields []string, limit int) ([]Issue, int, error) {
	return c.agileIssues(ctx, "/rest/agile/1.0/sprint/"+strconv.Itoa(sprintID)+"/issue", jql, fields, limit)
}

// BacklogIssues is SprintIssues for a board's backlog.
func (c *Client) BacklogIssues(ctx context.Context, boardID int, jql string, fields []string, limit int) ([]Issue, int, error) {
	return c.agileIssues(ctx, "/rest/agile/1.0/board/"+strconv.Itoa(boardID)+"/backlog", jql, fields, limit)
}

func (c *Client) agileIssues(ctx context.Context, path, jql string, fields []string, limit int) ([]Issue, int, error) {
	decode := func(raw []byte) ([]Issue, int, bool, error) {
		var page struct {
			Issues []Issue `json:"issues"`
			Total  int     `json:"total"`
		}
		err := json.Unmarshal(raw, &page)
		return page.Issues, page.Total, false, err
	}
	var probe json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, url.Values{"fields": {"summary"}, "maxResults": {"1"}}, nil, &probe); err != nil {
		return nil, 0, err
	}
	_, total, _, err := decode(probe)
	if err != nil {
		return nil, 0, err
	}
	q := url.Values{"fields": {strings.Join(fields, ",")}}
	if jql != "" {
		q.Set("jql", jql)
	}
	issues, err := startAtPages(ctx, c, path, q, limit, decode)
	return issues, total, err
}
