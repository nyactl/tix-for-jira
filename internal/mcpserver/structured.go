package mcpserver

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nyactl/approvekit/sanitize"
	"github.com/nyactl/tix-for-jira/internal/core"
)

// Read tools return their view twice: as Markdown text for the model, and
// as structured content with an output schema for clients that process
// results. Both hold the same redacted view, and both are sanitized.

type issuesOut struct {
	Issues []core.IssueSummary `json:"issues"`
}

type searchesOut struct {
	Searches map[string]string `json:"searches"`
}

type commentsOut struct {
	Comments []core.CommentView `json:"comments"`
}

type historyOut struct {
	Changes []core.ChangeView `json:"changes"`
}

type worklogsOut struct {
	Worklogs []core.WorklogView `json:"worklogs"`
}

type transitionsOut struct {
	Transitions []core.TransitionView `json:"transitions"`
}

type fieldsOut struct {
	Fields []core.EditableField `json:"fields"`
}

type linkTypesOut struct {
	LinkTypes []core.LinkTypeView `json:"linkTypes"`
}

type projectsOut struct {
	Projects []core.ProjectView `json:"projects"`
}

type epicsOut struct {
	Epics []core.Related `json:"epics"`
}

type issueTypesOut struct {
	IssueTypes []core.IssueTypeView `json:"issueTypes"`
}

type boardsOut struct {
	Boards []core.BoardView `json:"boards"`
}

type sprintsOut struct {
	Sprints []core.SprintView `json:"sprints"`
}

type sprintIssuesOut struct {
	Sprints []core.BoardIssues `json:"sprints"`
}

// structured returns markdown as the text content and v as the structured
// content, with every string in v sanitized like the text.
func structured[T any](markdown string, v T) (*mcp.CallToolResult, T, error) {
	clean, err := sanitized(v)
	if err != nil {
		var zero T
		return nil, zero, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: markdown}}}, clean, nil
}

// sanitized copies v with sanitize.String applied to every string, and
// empty lists instead of null, as the output schemas expect arrays.
func sanitized[T any](v T) (T, error) {
	var zero T
	raw, err := json.Marshal(v)
	if err != nil {
		return zero, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return zero, err
	}
	raw, err = json.Marshal(cleanValue(generic))
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return fillLists(out), nil
}

func cleanValue(v any) any {
	switch t := v.(type) {
	case string:
		return sanitize.String(t)
	case []any:
		for i := range t {
			t[i] = cleanValue(t[i])
		}
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[sanitize.String(k)] = cleanValue(val)
		}
		return out
	}
	return v
}

// fillLists replaces nil top-level lists of the wrapper types with empty
// ones, so the structured content always has the declared arrays.
func fillLists[T any](v T) T {
	switch w := any(v).(type) {
	case *issuesOut:
		if w != nil && w.Issues == nil {
			w.Issues = []core.IssueSummary{}
		}
	case *searchesOut:
		if w != nil && w.Searches == nil {
			w.Searches = map[string]string{}
		}
	case *commentsOut:
		if w != nil && w.Comments == nil {
			w.Comments = []core.CommentView{}
		}
	case *historyOut:
		if w != nil && w.Changes == nil {
			w.Changes = []core.ChangeView{}
		}
	case *worklogsOut:
		if w != nil && w.Worklogs == nil {
			w.Worklogs = []core.WorklogView{}
		}
	case *transitionsOut:
		if w != nil && w.Transitions == nil {
			w.Transitions = []core.TransitionView{}
		}
	case *fieldsOut:
		if w != nil && w.Fields == nil {
			w.Fields = []core.EditableField{}
		}
	case *linkTypesOut:
		if w != nil && w.LinkTypes == nil {
			w.LinkTypes = []core.LinkTypeView{}
		}
	case *projectsOut:
		if w != nil && w.Projects == nil {
			w.Projects = []core.ProjectView{}
		}
	case *epicsOut:
		if w != nil && w.Epics == nil {
			w.Epics = []core.Related{}
		}
	case *issueTypesOut:
		if w != nil && w.IssueTypes == nil {
			w.IssueTypes = []core.IssueTypeView{}
		}
	case *boardsOut:
		if w != nil && w.Boards == nil {
			w.Boards = []core.BoardView{}
		}
	case *sprintsOut:
		if w != nil && w.Sprints == nil {
			w.Sprints = []core.SprintView{}
		}
	case *sprintIssuesOut:
		if w != nil && w.Sprints == nil {
			w.Sprints = []core.BoardIssues{}
		}
	}
	return v
}
