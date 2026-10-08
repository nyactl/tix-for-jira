package jira

import "encoding/json"

// These types mirror the Jira Cloud REST v3 responses tix-jira reads. They
// carry raw personal data (account IDs, names, emails) and must never be
// printed directly; package core turns them into redacted views.

type User struct {
	AccountID    string `json:"accountId"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress,omitempty"`
	Active       bool   `json:"active"`
	TimeZone     string `json:"timeZone,omitempty"`
}

type StatusCategory struct {
	Key  string `json:"key"` // "new", "indeterminate" or "done"
	Name string `json:"name"`
}

type Status struct {
	ID             string         `json:"id,omitempty"`
	Name           string         `json:"name"`
	StatusCategory StatusCategory `json:"statusCategory"`
}

type IssueType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subtask bool   `json:"subtask"`
}

type Named struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type Project struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// RelatedIssue is the reduced issue Jira embeds for parents, subtasks and
// links. It never includes the assignee, so its scope cannot be checked
// without fetching it.
type RelatedIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary   string     `json:"summary"`
		Status    *Status    `json:"status"`
		IssueType *IssueType `json:"issuetype"`
		Priority  *Named     `json:"priority"`
	} `json:"fields"`
}

type LinkType struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

type IssueLink struct {
	ID           string        `json:"id"`
	Type         LinkType      `json:"type"`
	InwardIssue  *RelatedIssue `json:"inwardIssue,omitempty"`
	OutwardIssue *RelatedIssue `json:"outwardIssue,omitempty"`
}

type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Author   *User  `json:"author"`
	Created  string `json:"created"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

type TimeTracking struct {
	OriginalEstimate  string `json:"originalEstimate,omitempty"`
	RemainingEstimate string `json:"remainingEstimate,omitempty"`
	TimeSpent         string `json:"timeSpent,omitempty"`
}

// Fields holds the system fields tix-jira understands.
type Fields struct {
	Summary      string          `json:"summary"`
	Status       *Status         `json:"status"`
	IssueType    *IssueType      `json:"issuetype"`
	Priority     *Named          `json:"priority"`
	Resolution   *Named          `json:"resolution"`
	Assignee     *User           `json:"assignee"`
	Reporter     *User           `json:"reporter"`
	Project      *Project        `json:"project"`
	Created      string          `json:"created"`
	Updated      string          `json:"updated"`
	DueDate      string          `json:"duedate"`
	Labels       []string        `json:"labels"`
	Components   []Named         `json:"components"`
	Description  json.RawMessage `json:"description"`
	Parent       *RelatedIssue   `json:"parent"`
	Subtasks     []RelatedIssue  `json:"subtasks"`
	IssueLinks   []IssueLink     `json:"issuelinks"`
	Attachments  []Attachment    `json:"attachment"`
	TimeTracking *TimeTracking   `json:"timetracking"`
}

// Issue keeps both the typed system fields and every raw field value, so
// custom fields can be rendered by name.
type Issue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields Fields                     `json:"-"`
	Raw    map[string]json.RawMessage `json:"-"`
	Names  map[string]string          `json:"-"` // field ID -> display name, when requested
}

func (i *Issue) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID     string            `json:"id"`
		Key    string            `json:"key"`
		Fields json.RawMessage   `json:"fields"`
		Names  map[string]string `json:"names"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	i.ID, i.Key, i.Names = wire.ID, wire.Key, wire.Names
	if len(wire.Fields) == 0 || string(wire.Fields) == "null" {
		return nil
	}
	if err := json.Unmarshal(wire.Fields, &i.Fields); err != nil {
		return err
	}
	return json.Unmarshal(wire.Fields, &i.Raw)
}

type Comment struct {
	ID      string          `json:"id"`
	Author  *User           `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created string          `json:"created"`
	Updated string          `json:"updated"`
}

type ChangeItem struct {
	Field      string `json:"field"`
	FieldType  string `json:"fieldtype"`
	FieldID    string `json:"fieldId,omitempty"`
	From       string `json:"from"`
	FromString string `json:"fromString"`
	To         string `json:"to"`
	ToString   string `json:"toString"`
}

type ChangelogEntry struct {
	ID      string       `json:"id"`
	Author  *User        `json:"author"`
	Created string       `json:"created"`
	Items   []ChangeItem `json:"items"`
}

type Worklog struct {
	ID               string          `json:"id"`
	Author           *User           `json:"author"`
	Comment          json.RawMessage `json:"comment"`
	Started          string          `json:"started"`
	TimeSpent        string          `json:"timeSpent"`
	TimeSpentSeconds int             `json:"timeSpentSeconds"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   Status `json:"to"`
}

type Schema struct {
	Type     string `json:"type"`
	Items    string `json:"items,omitempty"`
	System   string `json:"system,omitempty"`
	Custom   string `json:"custom,omitempty"`
	CustomID int    `json:"customId,omitempty"`
}

// FieldMeta describes an editable or creatable field.
type FieldMeta struct {
	FieldID       string            `json:"fieldId,omitempty"`
	Key           string            `json:"key,omitempty"`
	Name          string            `json:"name"`
	Required      bool              `json:"required"`
	Schema        Schema            `json:"schema"`
	Operations    []string          `json:"operations,omitempty"`
	AllowedValues []json.RawMessage `json:"allowedValues,omitempty"`
}

// Field is an entry of the global field list.
type Field struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Custom bool    `json:"custom"`
	Schema *Schema `json:"schema,omitempty"`
}
