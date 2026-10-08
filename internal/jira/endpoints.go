package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Myself returns the authenticated user.
func (c *Client) Myself(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Search runs a JQL query via the token-paginated /search/jql endpoint and
// returns at most limit issues with the given fields.
func (c *Client) Search(ctx context.Context, jql string, fields []string, limit int) ([]Issue, error) {
	if limit <= 0 {
		return nil, nil
	}
	var all []Issue
	token := ""
	for len(all) < limit {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("fields", strings.Join(fields, ","))
		q.Set("maxResults", strconv.Itoa(min(limit-len(all), 100)))
		if token != "" {
			q.Set("nextPageToken", token)
		}
		var page struct {
			Issues        []Issue `json:"issues"`
			NextPageToken string  `json:"nextPageToken"`
			IsLast        bool    `json:"isLast"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/search/jql", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Issues...)
		if page.IsLast || page.NextPageToken == "" || len(page.Issues) == 0 {
			break
		}
		token = page.NextPageToken
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// GetIssue fetches one issue with all fields and the field name map.
func (c *Client) GetIssue(ctx context.Context, key string) (*Issue, error) {
	p, err := issuePath(key)
	if err != nil {
		return nil, err
	}
	q := url.Values{"fields": {"*all"}, "expand": {"names"}}
	var issue Issue
	if err := c.do(ctx, http.MethodGet, p, q, nil, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// startAtPages collects offset-paginated results until limit or the end.
func startAtPages[T any](ctx context.Context, c *Client, path string, extra url.Values, limit int, decode func([]byte) (items []T, total int, last bool, err error)) ([]T, error) {
	var all []T
	for len(all) < limit {
		q := url.Values{}
		for k, v := range extra {
			q[k] = v
		}
		q.Set("startAt", strconv.Itoa(len(all)))
		q.Set("maxResults", strconv.Itoa(min(limit-len(all), 100)))
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodGet, path, q, nil, &raw); err != nil {
			return nil, err
		}
		items, total, last, err := decode(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if last || len(items) == 0 || (total > 0 && len(all) >= total) {
			break
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// Comments returns up to limit comments, oldest first.
func (c *Client) Comments(ctx context.Context, key string, limit int) ([]Comment, error) {
	p, err := issuePath(key, "/comment")
	if err != nil {
		return nil, err
	}
	return startAtPages(ctx, c, p, url.Values{"orderBy": {"created"}}, limit, func(b []byte) ([]Comment, int, bool, error) {
		var page struct {
			Comments []Comment `json:"comments"`
			Total    int       `json:"total"`
		}
		err := json.Unmarshal(b, &page)
		return page.Comments, page.Total, false, err
	})
}

// AddComment posts an ADF comment.
func (c *Client) AddComment(ctx context.Context, key string, body json.RawMessage) (*Comment, error) {
	p, err := issuePath(key, "/comment")
	if err != nil {
		return nil, err
	}
	var out Comment
	if err := c.do(ctx, http.MethodPost, p, nil, map[string]any{"body": body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Changelog returns up to limit change history entries, oldest first.
func (c *Client) Changelog(ctx context.Context, key string, limit int) ([]ChangelogEntry, error) {
	p, err := issuePath(key, "/changelog")
	if err != nil {
		return nil, err
	}
	return startAtPages(ctx, c, p, nil, limit, func(b []byte) ([]ChangelogEntry, int, bool, error) {
		var page struct {
			Values []ChangelogEntry `json:"values"`
			Total  int              `json:"total"`
			IsLast bool             `json:"isLast"`
		}
		err := json.Unmarshal(b, &page)
		return page.Values, page.Total, page.IsLast, err
	})
}

// Worklogs returns up to limit worklogs, oldest first.
func (c *Client) Worklogs(ctx context.Context, key string, limit int) ([]Worklog, error) {
	p, err := issuePath(key, "/worklog")
	if err != nil {
		return nil, err
	}
	return startAtPages(ctx, c, p, nil, limit, func(b []byte) ([]Worklog, int, bool, error) {
		var page struct {
			Worklogs []Worklog `json:"worklogs"`
			Total    int       `json:"total"`
		}
		err := json.Unmarshal(b, &page)
		return page.Worklogs, page.Total, false, err
	})
}

// JiraTime formats t the way Jira expects for worklog start times.
func JiraTime(t time.Time) string { return t.Format("2006-01-02T15:04:05.000-0700") }

// AddWorklog logs time on an issue.
func (c *Client) AddWorklog(ctx context.Context, key string, seconds int, started time.Time, comment json.RawMessage) (*Worklog, error) {
	p, err := issuePath(key, "/worklog")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"timeSpentSeconds": seconds, "started": JiraTime(started)}
	if comment != nil {
		body["comment"] = comment
	}
	var out Worklog
	if err := c.do(ctx, http.MethodPost, p, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Transitions lists the transitions available to the user.
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	p, err := issuePath(key, "/transitions")
	if err != nil {
		return nil, err
	}
	var out struct {
		Transitions []Transition `json:"transitions"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Transitions, nil
}

// DoTransition moves an issue, optionally adding an ADF comment.
func (c *Client) DoTransition(ctx context.Context, key, transitionID string, comment json.RawMessage) error {
	p, err := issuePath(key, "/transitions")
	if err != nil {
		return err
	}
	if err := checkID(transitionID); err != nil {
		return err
	}
	body := map[string]any{"transition": map[string]string{"id": transitionID}}
	if comment != nil {
		body["update"] = map[string]any{"comment": []any{map[string]any{"add": map[string]any{"body": comment}}}}
	}
	return c.do(ctx, http.MethodPost, p, nil, body, nil)
}

// EditMeta returns the fields the user may edit on an issue, keyed by field ID.
func (c *Client) EditMeta(ctx context.Context, key string) (map[string]FieldMeta, error) {
	p, err := issuePath(key, "/editmeta")
	if err != nil {
		return nil, err
	}
	var out struct {
		Fields map[string]FieldMeta `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Fields, nil
}

// EditIssue sets field values (keyed by field ID).
func (c *Client) EditIssue(ctx context.Context, key string, fields map[string]any) error {
	p, err := issuePath(key)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, p, nil, map[string]any{"fields": fields}, nil)
}

// CreatableIssueTypes lists issue types the user can create in a project.
func (c *Client) CreatableIssueTypes(ctx context.Context, projectKey string) ([]IssueType, error) {
	pk, err := NormalizeProjectKey(projectKey)
	if err != nil {
		return nil, err
	}
	return startAtPages(ctx, c, "/rest/api/3/issue/createmeta/"+pk+"/issuetypes", nil, 200, func(b []byte) ([]IssueType, int, bool, error) {
		var page struct {
			IssueTypes []IssueType `json:"issueTypes"`
			Total      int         `json:"total"`
		}
		err := json.Unmarshal(b, &page)
		return page.IssueTypes, page.Total, false, err
	})
}

// CreateFields lists the fields of the create screen for a project and issue type.
func (c *Client) CreateFields(ctx context.Context, projectKey, issueTypeID string) ([]FieldMeta, error) {
	pk, err := NormalizeProjectKey(projectKey)
	if err != nil {
		return nil, err
	}
	if err := checkID(issueTypeID); err != nil {
		return nil, err
	}
	return startAtPages(ctx, c, "/rest/api/3/issue/createmeta/"+pk+"/issuetypes/"+issueTypeID, nil, 500, func(b []byte) ([]FieldMeta, int, bool, error) {
		var page struct {
			Fields []FieldMeta `json:"fields"`
			Total  int         `json:"total"`
		}
		err := json.Unmarshal(b, &page)
		return page.Fields, page.Total, false, err
	})
}

// CreateIssue creates an issue and returns its key.
func (c *Client) CreateIssue(ctx context.Context, fields map[string]any) (string, error) {
	var out struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue", nil, map[string]any{"fields": fields}, &out); err != nil {
		return "", err
	}
	if out.Key == "" {
		return "", errors.New("Jira did not return the new issue key")
	}
	return out.Key, nil
}

// LinkTypes lists the issue link types of the site.
func (c *Client) LinkTypes(ctx context.Context) ([]LinkType, error) {
	var out struct {
		IssueLinkTypes []LinkType `json:"issueLinkTypes"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issueLinkType", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.IssueLinkTypes, nil
}

// CreateLink links two issues. In Jira's model the outward issue is the
// subject of the link type's outward phrase: for type "Blocks", outward A
// and inward B reads "A blocks B".
func (c *Client) CreateLink(ctx context.Context, typeName, outwardKey, inwardKey string) error {
	out, err := NormalizeKey(outwardKey)
	if err != nil {
		return err
	}
	in, err := NormalizeKey(inwardKey)
	if err != nil {
		return err
	}
	body := map[string]any{
		"type":         map[string]string{"name": typeName},
		"outwardIssue": map[string]string{"key": out},
		"inwardIssue":  map[string]string{"key": in},
	}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issueLink", nil, body, nil)
}

// Projects lists projects the user may perform action on ("browse" or "create").
func (c *Client) Projects(ctx context.Context, action string, limit int) ([]Project, error) {
	return startAtPages(ctx, c, "/rest/api/3/project/search", url.Values{"action": {action}}, limit, func(b []byte) ([]Project, int, bool, error) {
		var page struct {
			Values []Project `json:"values"`
			Total  int       `json:"total"`
			IsLast bool      `json:"isLast"`
		}
		err := json.Unmarshal(b, &page)
		return page.Values, page.Total, page.IsLast, err
	})
}

// Fields lists every field visible to the user.
func (c *Client) Fields(ctx context.Context) ([]Field, error) {
	var out []Field
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/field", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrTooLarge is returned when an attachment exceeds the download limit.
var ErrTooLarge = errors.New("attachment exceeds the download limit")

// DownloadAttachment streams an attachment's content to w. redirect=false
// makes Jira return the bytes itself instead of redirecting to its media
// host, so the request never leaves the site.
func (c *Client) DownloadAttachment(ctx context.Context, id string, w io.Writer, limit int64) (int64, error) {
	if err := checkID(id); err != nil {
		return 0, err
	}
	resp, err := c.send(ctx, http.MethodGet, "/rest/api/3/attachment/content/"+id, url.Values{"redirect": {"false"}}, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("%w (%d bytes)", ErrTooLarge, limit)
	}
	return n, nil
}
