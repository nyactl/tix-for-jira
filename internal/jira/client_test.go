package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "me@example.com", "secret", "test")
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestRequestHeaders(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("me@example.com:secret"))
		if r.Header.Get("Authorization") != wantAuth {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") != "tix-for-jira/test" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		writeJSON(w, 200, User{AccountID: "acc-me", DisplayName: "Me Myself"})
	}))
	u, err := c.Myself(context.Background())
	if err != nil || u.AccountID != "acc-me" {
		t.Fatalf("Myself = %+v, %v", u, err)
	}
}

func TestSearchPaginatesWithTokens(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("fields") != "summary,status" || q.Get("jql") != "project = A" {
			t.Errorf("query = %v", q)
		}
		switch q.Get("nextPageToken") {
		case "":
			writeJSON(w, 200, map[string]any{"issues": []any{map[string]any{"key": "A-1"}, map[string]any{"key": "A-2"}}, "nextPageToken": "t2"})
		case "t2":
			if q.Get("maxResults") != "1" {
				t.Errorf("maxResults on page 2 = %s", q.Get("maxResults"))
			}
			writeJSON(w, 200, map[string]any{"issues": []any{map[string]any{"key": "A-3"}}, "nextPageToken": "t3"})
		default:
			t.Errorf("unexpected token %q", q.Get("nextPageToken"))
		}
	}))
	issues, err := c.Search(context.Background(), "project = A", []string{"summary", "status"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 || issues[2].Key != "A-3" || calls.Load() != 2 {
		t.Errorf("got %d issues in %d calls", len(issues), calls.Load())
	}
}

func TestGetIssueKeepsRawFieldsAndNames(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/PROJ-7" || r.URL.Query().Get("fields") != "*all" || r.URL.Query().Get("expand") != "names" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"id":"10007","key":"PROJ-7",
			"fields":{"summary":"S","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},
			"assignee":{"accountId":"acc-me","displayName":"Me"},"customfield_10016":5},
			"names":{"customfield_10016":"Story Points"}}`)
	}))
	issue, err := c.GetIssue(context.Background(), "proj-7")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Fields.Status.StatusCategory.Key != "indeterminate" || issue.Fields.Assignee.AccountID != "acc-me" {
		t.Errorf("typed fields = %+v", issue.Fields)
	}
	if string(issue.Raw["customfield_10016"]) != "5" || issue.Names["customfield_10016"] != "Story Points" {
		t.Errorf("raw = %v names = %v", issue.Raw, issue.Names)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		body   string
		check  func(error) bool
		desc   string
	}{
		{401, ``, func(err error) bool { return errors.Is(err, ErrUnauthorized) }, "unauthorized"},
		{404, `{"errorMessages":["Issue does not exist"]}`, func(err error) bool { return errors.Is(err, ErrNotFound) }, "not found"},
		{403, `{"errorMessages":["You do not have permission"]}`, func(err error) bool {
			return strings.Contains(err.Error(), "permission denied by Jira: You do not have permission")
		}, "forbidden"},
		{400, `{"errorMessages":["bad"],"errors":{"summary":"required","labels":"invalid"}}`, func(err error) bool {
			var apiErr *APIError
			return errors.As(err, &apiErr) && err.Error() == "Jira returned HTTP 400: bad; labels: invalid; summary: required"
		}, "bad request with field errors"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			_, err := c.Myself(context.Background())
			if err == nil || !tt.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestRetriesRateLimit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var waited []time.Duration
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, 200, User{AccountID: "acc-me"})
	}))
	c.sleep = func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }
	if _, err := c.Myself(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(waited) != 2 || waited[0] != 2*time.Second {
		t.Errorf("calls = %d waited = %v", calls.Load(), waited)
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	if _, err := c.Myself(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != maxRetries+1 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestRefusesCrossHostRedirect(t *testing.T) {
	t.Parallel()
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	t.Cleanup(other.Close)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	_, err := c.Myself(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("err = %v", err)
	}
	if leaked.Load() {
		t.Error("request reached the other host")
	}
}

func TestNormalizeKey(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"PROJ-1", "proj-12", " AB_2-9 "} {
		if _, err := NormalizeKey(k); err != nil {
			t.Errorf("NormalizeKey(%q) = %v", k, err)
		}
	}
	for _, k := range []string{"", "PROJ", "PROJ-0", "1A-1", "PROJ-1/../x", "..", "PROJ-1?x", "10001"} {
		if _, err := NormalizeKey(k); err == nil {
			t.Errorf("NormalizeKey(%q) accepted", k)
		}
	}
}

func TestInvalidKeyMakesNoRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	ctx := context.Background()
	if _, err := c.GetIssue(ctx, "../myself"); err == nil {
		t.Error("GetIssue accepted bad key")
	}
	if err := c.EditIssue(ctx, "..", nil); err == nil {
		t.Error("EditIssue accepted bad key")
	}
	if err := c.DoTransition(ctx, "A-1", "../1"); err == nil {
		t.Error("DoTransition accepted bad transition id")
	}
	if _, err := c.DownloadAttachment(ctx, "../1", io.Discard, 10); err == nil {
		t.Error("DownloadAttachment accepted bad id")
	}
	if calls.Load() != 0 {
		t.Errorf("server received %d requests", calls.Load())
	}
}

func TestDownloadAttachment(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/attachment/content/42" || r.URL.Query().Get("redirect") != "false" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, "0123456789")
	}))
	var buf bytes.Buffer
	n, err := c.DownloadAttachment(context.Background(), "42", &buf, 10)
	if err != nil || n != 10 || buf.String() != "0123456789" {
		t.Fatalf("n=%d err=%v body=%q", n, err, buf.String())
	}
	_, err = c.DownloadAttachment(context.Background(), "42", io.Discard, 5)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestWriteBodies(t *testing.T) {
	t.Parallel()
	var got []map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["_route"] = r.Method + " " + r.URL.Path
		got = append(got, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := context.Background()
	if err := c.DoTransition(ctx, "A-1", "31"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateLink(ctx, "Blocks", "A-1", "A-2"); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 8, 9, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	_, _ = c.AddWorklog(ctx, "A-1", 5400, started, nil)

	if got[0]["_route"] != "POST /rest/api/3/issue/A-1/transitions" || got[0]["transition"].(map[string]any)["id"] != "31" {
		t.Errorf("transition body = %v", got[0])
	}
	if _, ok := got[0]["update"]; ok {
		t.Errorf("transition must not carry a comment: %v", got[0])
	}
	if got[1]["inwardIssue"].(map[string]any)["key"] != "A-1" || got[1]["outwardIssue"].(map[string]any)["key"] != "A-2" {
		t.Errorf("link body = %v", got[1])
	}
	if got[2]["started"] != "2026-10-08T09:30:00.000+0200" || got[2]["timeSpentSeconds"] != float64(5400) {
		t.Errorf("worklog body = %v", got[2])
	}
}
