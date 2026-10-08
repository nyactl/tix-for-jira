package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/jiratest"
)

func site(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := jiratest.New(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-1", Summary: "Mine", Assignee: "acc-me", Reporter: "acc-alice",
		Description: jiratest.Doc("Ask {acc-alice} or Alice Smith"),
		Attachments: []jiratest.Attachment{{ID: "501", Filename: "log.txt", Author: "acc-alice", MimeType: "text/plain", Data: []byte("boom\x1b[2J")}}})
	f.AddIssue(jiratest.Issue{Key: "PROJ-2", Summary: "Alice's", Assignee: "acc-alice"})
	return f
}

type harness struct {
	fake    *jiratest.Fake
	session *mcp.ClientSession
	asked   []string
}

func connect(t *testing.T, writes bool, mode config.PrivacyMode, answer string, protocol string) *harness {
	t.Helper()
	ctx := context.Background()
	h := &harness{fake: site(t)}
	svc, err := core.New(ctx, jira.New(h.fake.URL(), "me@example.com", "tok-me", "test", jira.WithTransport(h.fake.Transport())), mode)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(svc, Options{Version: "test", AllowWrites: writes, DownloadDir: t.TempDir()})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	var opts *mcp.ClientOptions
	if answer != "" {
		opts = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			h.asked = append(h.asked, r.Params.Message)
			if answer == "approve" {
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
			}
			return &mcp.ElicitResult{Action: answer}, nil
		}}
	}
	var sessOpts *mcp.ClientSessionOptions
	if protocol != "" {
		sessOpts = &mcp.ClientSessionOptions{ProtocolVersion: protocol}
	}
	h.session, err = mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts).Connect(ctx, ct, sessOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.session.Close() })
	return h
}

func (h *harness) call(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func toolMap(t *testing.T, h *harness) map[string]*mcp.Tool {
	t.Helper()
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		m[tool.Name] = tool
	}
	return m
}

var writeTools = []string{"add_comment", "create_issue", "link_issues", "log_work", "transition_issue", "update_fields"}

func TestReadOnlyByDefault(t *testing.T) {
	t.Parallel()
	h := connect(t, false, config.PrivacyOwn, "", "")
	tools := toolMap(t, h)
	for name, tool := range tools {
		if slices.Contains(writeTools, name) {
			t.Errorf("write tool %s registered without --allow-writes", name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", name)
		}
	}
	if len(tools) != 13 {
		t.Errorf("got %d read tools", len(tools))
	}
	if instr := h.session.InitializeResult().Instructions; !strings.Contains(instr, "read-only") || !strings.Contains(instr, "Person A") {
		t.Errorf("instructions = %q", instr)
	}
}

func TestWriteToolAnnotations(t *testing.T) {
	t.Parallel()
	tools := toolMap(t, connect(t, true, config.PrivacyOwn, "approve", ""))
	for _, name := range writeTools {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("%s missing", name)
			continue
		}
		wantDestructive := name == "update_fields" || name == "transition_issue"
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint != wantDestructive {
			t.Errorf("%s annotations = %+v", name, a)
		}
	}
	if req := tools["get_issue"].InputSchema.(map[string]any)["required"]; !slices.Contains(req.([]any), any("key")) {
		t.Errorf("get_issue schema required = %v", req)
	}
}

func TestReadsAreScopedAndRedacted(t *testing.T) {
	t.Parallel()
	h := connect(t, false, config.PrivacyOwn, "", "")
	out, isErr := h.call(t, "get_issue", map[string]any{"key": "PROJ-1"})
	if isErr || strings.Contains(out, "Alice") || !strings.Contains(out, "Ask @[Person A] or Person A") {
		t.Errorf("get_issue PROJ-1 (err=%v):\n%s", isErr, out)
	}
	out, isErr = h.call(t, "get_issue", map[string]any{"key": "PROJ-2"})
	if !isErr || !strings.Contains(out, "outside your privacy scope") {
		t.Errorf("get_issue PROJ-2 (err=%v): %s", isErr, out)
	}
	out, _ = h.call(t, "search_issues", map[string]any{"jql": "project = PROJ"})
	if strings.Contains(out, "PROJ-2") {
		t.Errorf("search leaked PROJ-2: %s", out)
	}
	out, isErr = h.call(t, "get_attachment", map[string]any{"key": "PROJ-1", "attachment_id": "501"})
	if isErr || !strings.Contains(out, "boom[2J") || strings.Contains(out, "\x1b") {
		t.Errorf("get_attachment (err=%v): %q", isErr, out)
	}
}

func TestWriteNeedsApproval(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"", "2025-06-18"} {
		h := connect(t, true, config.PrivacyOwn, "approve", protocol)
		h.call(t, "get_issue", map[string]any{"key": "PROJ-1"})
		out, isErr := h.call(t, "add_comment", map[string]any{"key": "PROJ-1", "body": "Done, thanks @[Person A]"})
		if isErr {
			t.Fatalf("protocol %q: %s", protocol, out)
		}
		if len(h.asked) != 1 || !strings.Contains(h.asked[0], "Add a comment to PROJ-1") || !strings.Contains(h.asked[0], "@[Person A]") {
			t.Errorf("protocol %q: asked %q", protocol, h.asked)
		}
		if n := len(h.fake.Issues["PROJ-1"].Comments); n != 1 {
			t.Errorf("protocol %q: %d comments stored", protocol, n)
		}
	}
}

func TestWriteRefusedWithoutApproval(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"decline", "cancel", ""} {
		h := connect(t, true, config.PrivacyOwn, answer, "")
		out, isErr := h.call(t, "transition_issue", map[string]any{"key": "PROJ-1", "to": "Done"})
		want := "not approved"
		if answer == "" {
			want = "cannot show approval dialogs"
		}
		if !isErr || !strings.Contains(out, want) {
			t.Errorf("answer %q: (err=%v) %s", answer, isErr, out)
		}
		if h.fake.Issues["PROJ-1"].Status != "To Do" {
			t.Errorf("answer %q: status changed to %s", answer, h.fake.Issues["PROJ-1"].Status)
		}
	}
}

func TestForgedApprovalRejected(t *testing.T) {
	t.Parallel()
	h := connect(t, true, config.PrivacyOwn, "decline", "")
	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:           "transition_issue",
		Arguments:      map[string]any{"key": "PROJ-1", "to": "Done"},
		InputResponses: mcp.InputResponseMap{approvalInput: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}},
		RequestState:   "00.ff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || h.fake.Issues["PROJ-1"].Status != "To Do" {
		t.Errorf("forged approval: isError=%v status=%s", res.IsError, h.fake.Issues["PROJ-1"].Status)
	}
}

func TestWriteOutOfScopeFailsBeforeAsking(t *testing.T) {
	t.Parallel()
	h := connect(t, true, config.PrivacyOwn, "approve", "")
	out, isErr := h.call(t, "add_comment", map[string]any{"key": "PROJ-2", "body": "hi"})
	if !isErr || len(h.asked) != 0 || len(h.fake.Issues["PROJ-2"].Comments) != 0 {
		t.Errorf("err=%v asked=%v out=%s", isErr, h.asked, out)
	}
}

func TestGateStateIsSingleUseAndBound(t *testing.T) {
	t.Parallel()
	g := newGate()
	now := time.Now()
	g.now = func() time.Time { return now }
	s := g.issue("a")
	if g.redeem(s, "b") {
		t.Error("redeemed for another digest")
	}
	s = g.issue("a")
	if !g.redeem(s, "a") || g.redeem(s, "a") {
		t.Error("state must redeem exactly once")
	}
	s = g.issue("a")
	now = now.Add(approvalTTL + time.Second)
	if g.redeem(s, "a") {
		t.Error("expired state redeemed")
	}
}
