//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/mcpserver"
)

// TestMCPPromptsAndStructuredContent serves the MCP tools for the test site
// in memory, follows the steps the prompts name for a new ticket, and checks
// every result against the tool's output schema.
func TestMCPPromptsAndStructuredContent(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	msg := applied(t)(s.svc.PlanCreate(ctx, core.CreateInput{Project: s.project, Type: "Task",
		Summary: "tix-for-jira mcp " + s.run, Description: "For the MCP prompt check.", Labels: []string{"tix-for-jira-it"}}))
	key := regexp.MustCompile(`[A-Z][A-Z0-9_]*-[0-9]+`).FindString(msg)
	if key == "" {
		t.Fatalf("no key in %q", msg)
	}
	closeWhenDone(t, s.jc, key)

	srv := mcpserver.New(s.svc, mcpserver.Options{Version: "integration", DownloadDir: t.TempDir()})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]*jsonschema.Resolved{}
	for _, tool := range list.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		raw, _ := json.Marshal(tool.OutputSchema)
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if schemas[tool.Name], err = schema.Resolve(nil); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
	}

	call := func(name string, args map[string]any) string {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		var instance any
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &instance)
		if schemas[name] == nil {
			t.Fatalf("%s has no output schema", name)
		}
		if err := schemas[name].Validate(instance); err != nil {
			t.Errorf("%s: structured content does not match its schema: %v", name, err)
		}
		return string(b)
	}

	t.Run("ticket-summary", func(t *testing.T) {
		p, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "ticket-summary", Arguments: map[string]string{"key": key}})
		if err != nil {
			t.Fatal(err)
		}
		text := p.Messages[0].Content.(*mcp.TextContent).Text
		tools := regexp.MustCompile(`\b(get|list)_[a-z_]+\b`).FindAllString(text, -1)
		if len(tools) < 4 {
			t.Fatalf("prompt names %v", tools)
		}
		for _, tool := range tools {
			out := call(tool, map[string]any{"key": key})
			if tool == "get_issue" && !strings.Contains(out, `"key":"`+key+`"`) {
				t.Errorf("get_issue structured content: %s", out)
			}
		}
	})

	t.Run("morning-overview", func(t *testing.T) {
		p, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "morning-overview"})
		if err != nil {
			t.Fatal(err)
		}
		if text := p.Messages[0].Content.(*mcp.TextContent).Text; !strings.Contains(text, "my_issues") {
			t.Fatalf("prompt: %s", text)
		}
		if out := call("my_issues", map[string]any{"since": "-1d", "include_done": true}); !strings.Contains(out, key) {
			t.Errorf("my_issues since -1d does not list %s: %s", key, out)
		}
	})

	t.Run("other structured tools", func(t *testing.T) {
		call("whoami", map[string]any{})
		call("list_projects", map[string]any{})
		call("list_issue_types", map[string]any{"project": s.project})
		call("list_transitions", map[string]any{"key": key})
		call("list_editable_fields", map[string]any{"key": key})
		call("list_boards", map[string]any{"project": s.project})
	})
}
