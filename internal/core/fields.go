package core

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nyactl/tix-for-jira/internal/jira"
)

type kind int

const (
	kindUnsupported kind = iota
	kindText
	kindRich
	kindNumber
	kindDate
	kindDateTime
	kindLabels
	kindOption
	kindOptions
	kindPriority
	kindComponents
	kindVersions
)

func (k kind) String() string {
	return [...]string{"unsupported", "text", "markdown", "number", "date (YYYY-MM-DD)", "date-time", "labels (comma-separated)", "choice", "choices (comma-separated)", "priority", "components (comma-separated)", "versions (comma-separated)"}[k]
}

// fieldKind decides how a field is written. People fields are deliberately
// unsupported: changing who a ticket belongs to is outside tix-for-jira's scope.
func fieldKind(id string, m jira.FieldMeta) kind {
	sch := m.Schema
	if !contains(m.Operations, "set") {
		return kindUnsupported
	}
	switch {
	case sch.System == "description" || sch.System == "environment" || strings.HasSuffix(sch.Custom, ":textarea"):
		return kindRich
	case sch.Type == "string":
		return kindText
	case sch.Type == "number":
		return kindNumber
	case sch.Type == "date":
		return kindDate
	case sch.Type == "datetime":
		return kindDateTime
	case sch.Type == "option":
		return kindOption
	case sch.Type == "priority":
		return kindPriority
	case sch.Type == "array" && sch.Items == "string":
		return kindLabels
	case sch.Type == "array" && sch.Items == "option":
		return kindOptions
	case sch.Type == "array" && sch.Items == "component":
		return kindComponents
	case sch.Type == "array" && sch.Items == "version":
		return kindVersions
	}
	return kindUnsupported
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type allowed struct{ id, name string }

func allowedValues(raw []json.RawMessage) []allowed {
	out := make([]allowed, 0, len(raw))
	for _, r := range raw {
		var v struct {
			ID    string `json:"id"`
			Value string `json:"value"`
			Name  string `json:"name"`
		}
		if json.Unmarshal(r, &v) != nil {
			continue
		}
		name := v.Value
		if name == "" {
			name = v.Name
		}
		if name != "" && v.ID != "" {
			out = append(out, allowed{v.ID, name})
		}
	}
	return out
}

func allowedNames(raw []json.RawMessage, limit int) []string {
	var out []string
	for _, a := range allowedValues(raw) {
		if len(out) == limit {
			out = append(out, "…")
			break
		}
		out = append(out, a.name)
	}
	return out
}

func pick(raw []json.RawMessage, input string) (string, error) {
	vals := allowedValues(raw)
	for _, a := range vals {
		if strings.EqualFold(a.name, strings.TrimSpace(input)) {
			return a.id, nil
		}
	}
	names := make([]string, len(vals))
	for i, a := range vals {
		names[i] = a.name
	}
	return "", fmt.Errorf("%q is not allowed; choose one of: %s", input, strings.Join(names, ", "))
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// convert turns user input into the JSON value Jira expects for the field.
// An empty input clears the field.
func (s *Service) convert(k kind, m jira.FieldMeta, input string) (any, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		if m.Required {
			return nil, fmt.Errorf("%s is required and cannot be cleared", m.Name)
		}
		if k == kindLabels || k == kindOptions || k == kindComponents || k == kindVersions {
			return []any{}, nil
		}
		return nil, nil
	}
	switch k {
	case kindText:
		return input, nil
	case kindRich:
		return s.toADF(input)
	case kindNumber:
		f, err := strconv.ParseFloat(input, 64)
		if err != nil {
			return nil, fmt.Errorf("%s needs a number, got %q", m.Name, input)
		}
		return f, nil
	case kindDate:
		if _, err := time.Parse(time.DateOnly, input); err != nil {
			return nil, fmt.Errorf("%s needs a date like 2026-10-31, got %q", m.Name, input)
		}
		return input, nil
	case kindDateTime:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04"} {
			if t, err := time.ParseInLocation(layout, input, s.now().Location()); err == nil {
				return jira.JiraTime(t), nil
			}
		}
		return nil, fmt.Errorf("%s needs a date and time like 2026-10-31 14:00, got %q", m.Name, input)
	case kindLabels:
		labels := splitList(input)
		for _, l := range labels {
			if strings.ContainsAny(l, " \t") {
				return nil, fmt.Errorf("label %q must not contain spaces", l)
			}
		}
		return labels, nil
	case kindOption, kindPriority:
		id, err := pick(m.AllowedValues, input)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		return map[string]string{"id": id}, nil
	case kindOptions, kindComponents, kindVersions:
		var out []map[string]string
		for _, part := range splitList(input) {
			id, err := pick(m.AllowedValues, part)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m.Name, err)
			}
			out = append(out, map[string]string{"id": id})
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s cannot be changed with tix-for-jira", m.Name)
}

// findField matches a field by ID or case-insensitive name.
func findField(meta map[string]jira.FieldMeta, name string) (string, jira.FieldMeta, error) {
	if m, ok := meta[name]; ok {
		return name, m, nil
	}
	var ids []string
	for id, m := range meta {
		if strings.EqualFold(m.Name, strings.TrimSpace(name)) {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return "", jira.FieldMeta{}, fmt.Errorf("field %q is not editable on this ticket; list editable fields first", name)
	case 1:
		return ids[0], meta[ids[0]], nil
	}
	return "", jira.FieldMeta{}, fmt.Errorf("field name %q is ambiguous; use one of the IDs %s", name, strings.Join(ids, ", "))
}
