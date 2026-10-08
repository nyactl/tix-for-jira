package core

// Views contain only text that is safe to show: people are labels, rich
// text is Markdown, and no account IDs, emails or avatar URLs appear.

type IssueSummary struct {
	Key            string   `json:"key"`
	Summary        string   `json:"summary"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"statusCategory"` // new, indeterminate, done
	Priority       string   `json:"priority,omitempty"`
	Due            string   `json:"due,omitempty"`
	Updated        string   `json:"updated"`
	Labels         []string `json:"labels,omitempty"`
}

// Related is a parent, subtask or linked ticket. In own mode its summary is
// omitted, because Jira does not say who it is assigned to.
type Related struct {
	Relation string `json:"relation"`
	Key      string `json:"key"`
	Type     string `json:"type,omitempty"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type AttachmentInfo struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Author   string `json:"author"`
	Created  string `json:"created"`
}

type FieldValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TimeTracking struct {
	Original  string `json:"original,omitempty"`
	Remaining string `json:"remaining,omitempty"`
	Spent     string `json:"spent,omitempty"`
}

type IssueDetail struct {
	IssueSummary
	URL          string           `json:"url"`
	Project      string           `json:"project"`
	Assignee     string           `json:"assignee"`
	Reporter     string           `json:"reporter,omitempty"`
	Created      string           `json:"created"`
	Resolution   string           `json:"resolution,omitempty"`
	Components   []string         `json:"components,omitempty"`
	Description  string           `json:"description,omitempty"`
	Parent       *Related         `json:"parent,omitempty"`
	Subtasks     []Related        `json:"subtasks,omitempty"`
	Links        []Related        `json:"links,omitempty"`
	Attachments  []AttachmentInfo `json:"attachments,omitempty"`
	TimeTracking *TimeTracking    `json:"timeTracking,omitempty"`
	Fields       []FieldValue     `json:"fields,omitempty"`
}

type CommentView struct {
	ID      string `json:"id"`
	Author  string `json:"author"`
	Created string `json:"created"`
	Edited  bool   `json:"edited,omitempty"`
	Body    string `json:"body"`
}

type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

type ChangeView struct {
	Author  string        `json:"author"`
	When    string        `json:"when"`
	Changes []FieldChange `json:"changes"`
}

type WorklogView struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Started   string `json:"started"`
	TimeSpent string `json:"timeSpent"`
	Comment   string `json:"comment,omitempty"`
}

type TransitionView struct {
	Name     string `json:"name"`
	To       string `json:"to"`
	Category string `json:"category"`
}

type EditableField struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Allowed  []string `json:"allowed,omitempty"`
}

type LinkTypeView struct {
	Name    string `json:"name"`
	Outward string `json:"outward"`
	Inward  string `json:"inward"`
}

type ProjectView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type IssueTypeView struct {
	Name    string `json:"name"`
	Subtask bool   `json:"subtask,omitempty"`
}

type Me struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
	Site        string `json:"site"`
	Privacy     string `json:"privacy"`
}

// AttachmentFile is a downloaded attachment.
type AttachmentFile struct {
	AttachmentInfo
	Path string `json:"path"`
	// Data holds the content for small text and image files, for inline use.
	Data []byte `json:"-"`
}
