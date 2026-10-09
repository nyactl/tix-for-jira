// Package adf converts between Atlassian Document Format, which Jira Cloud
// uses for rich text, and Markdown.
package adf

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Node is an ADF node. Only the fields tix-for-jira uses are modelled.
type Node struct {
	Type    string         `json:"type"`
	Version int            `json:"version,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []Node         `json:"content,omitempty"`
	Text    string         `json:"text,omitempty"`
	Marks   []Mark         `json:"marks,omitempty"`
}

type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// MentionFunc returns the name shown for a mention of accountID; fallback
// is the text Jira stored with the mention.
type MentionFunc func(accountID, fallback string) string

// ToMarkdown renders an ADF document as Markdown. Empty or null input
// yields "". Unknown node types fall back to their text content.
func ToMarkdown(raw json.RawMessage, mention MentionFunc) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", nil
	}
	// Some fields still carry plain strings; keep them as-is.
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return "", err
		}
		return str, nil
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("invalid ADF: %w", err)
	}
	if mention == nil {
		mention = func(_, fallback string) string { return strings.TrimPrefix(fallback, "@") }
	}
	r := renderer{mention: mention}
	return strings.TrimSpace(r.blocks(doc.Content)), nil
}

type renderer struct {
	mention MentionFunc
}

func (r renderer) blocks(nodes []Node) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s := r.block(n); strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (r renderer) block(n Node) string {
	switch n.Type {
	case "paragraph":
		return r.inline(n.Content)
	case "heading":
		level := min(max(intAttr(n.Attrs, "level", 1), 1), 6)
		return strings.Repeat("#", level) + " " + r.inline(n.Content)
	case "bulletList":
		return r.list(n.Content, func(int) string { return "- " })
	case "orderedList":
		start := intAttr(n.Attrs, "order", 1)
		return r.list(n.Content, func(i int) string { return strconv.Itoa(start+i) + ". " })
	case "taskList":
		return r.list(n.Content, func(int) string { return "" })
	case "taskItem":
		box := "- [ ] "
		if strAttr(n.Attrs, "state") == "DONE" {
			box = "- [x] "
		}
		return box + r.inline(n.Content)
	case "decisionList":
		return r.list(n.Content, func(int) string { return "- Decision: " })
	case "decisionItem":
		return r.inline(n.Content)
	case "codeBlock":
		return "```" + strAttr(n.Attrs, "language") + "\n" + plainText(n.Content) + "\n```"
	case "blockquote":
		return quote(r.blocks(n.Content))
	case "panel":
		label := strAttr(n.Attrs, "panelType")
		if label == "" {
			label = "note"
		}
		return quote("**" + strings.ToUpper(label[:1]) + label[1:] + ":** " + r.blocks(n.Content))
	case "rule":
		return "---"
	case "table":
		return r.table(n)
	case "expand", "nestedExpand":
		title := strAttr(n.Attrs, "title")
		body := r.blocks(n.Content)
		if title == "" {
			return body
		}
		return "**" + title + "**\n\n" + body
	case "mediaSingle", "mediaGroup":
		names := make([]string, 0, len(n.Content))
		for _, m := range n.Content {
			names = append(names, mediaLabel(m))
		}
		return strings.Join(names, " ")
	case "media":
		return mediaLabel(n)
	case "blockCard", "embedCard":
		return "<" + strAttr(n.Attrs, "url") + ">"
	default:
		if len(n.Content) > 0 {
			if isInline(n.Content[0]) {
				return r.inline(n.Content)
			}
			return r.blocks(n.Content)
		}
		return r.inline([]Node{n})
	}
}

func (r renderer) list(items []Node, marker func(int) string) string {
	lines := make([]string, 0, len(items))
	for i, item := range items {
		if item.Type == "taskItem" || item.Type == "decisionItem" {
			lines = append(lines, marker(i)+r.block(item))
			continue
		}
		body := r.blocks(item.Content)
		prefix := marker(i)
		indent := strings.Repeat(" ", len(prefix))
		bodyLines := strings.Split(body, "\n")
		for j, l := range bodyLines {
			switch {
			case j == 0:
				bodyLines[j] = prefix + l
			case l == "":
				bodyLines[j] = ""
			default:
				bodyLines[j] = indent + l
			}
		}
		lines = append(lines, strings.Join(bodyLines, "\n"))
	}
	return strings.Join(lines, "\n")
}

func (r renderer) table(n Node) string {
	var rows [][]string
	for _, row := range n.Content {
		var cells []string
		for _, cell := range row.Content {
			text := strings.ReplaceAll(r.blocks(cell.Content), "\n\n", "<br>")
			text = strings.ReplaceAll(text, "\n", "<br>")
			cells = append(cells, strings.ReplaceAll(text, "|", `\|`))
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for i := range cols {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString(" " + c + " |")
		}
		b.WriteString("\n")
	}
	writeRow(rows[0])
	b.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
	for _, row := range rows[1:] {
		writeRow(row)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r renderer) inline(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(applyMarks(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			b.WriteString("@[" + r.mention(strAttr(n.Attrs, "id"), strAttr(n.Attrs, "text")) + "]")
		case "emoji":
			if t := strAttr(n.Attrs, "text"); t != "" {
				b.WriteString(t)
			} else {
				b.WriteString(strAttr(n.Attrs, "shortName"))
			}
		case "inlineCard":
			b.WriteString("<" + strAttr(n.Attrs, "url") + ">")
		case "date":
			b.WriteString(formatDate(n.Attrs["timestamp"]))
		case "status":
			b.WriteString("[" + strAttr(n.Attrs, "text") + "]")
		case "placeholder":
		default:
			if len(n.Content) > 0 {
				b.WriteString(r.inline(n.Content))
			} else {
				b.WriteString(n.Text)
			}
		}
	}
	return b.String()
}

func applyMarks(text string, marks []Mark) string {
	if text == "" {
		return ""
	}
	var link string
	code := false
	for _, m := range marks {
		switch m.Type {
		case "code":
			code = true
		case "link":
			link = strAttr(m.Attrs, "href")
		}
	}
	if code {
		text = "`" + text + "`"
	} else {
		for _, m := range marks {
			switch m.Type {
			case "strong":
				text = "**" + text + "**"
			case "em":
				text = "*" + text + "*"
			case "strike":
				text = "~~" + text + "~~"
			}
		}
	}
	if link != "" {
		text = "[" + text + "](" + link + ")"
	}
	return text
}

func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

func mediaLabel(n Node) string {
	if alt := strAttr(n.Attrs, "alt"); alt != "" {
		return "[attachment: " + alt + "]"
	}
	return "[attachment]"
}

func plainText(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == "hardBreak" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(n.Text)
		b.WriteString(plainText(n.Content))
	}
	return b.String()
}

func isInline(n Node) bool {
	switch n.Type {
	case "text", "hardBreak", "mention", "emoji", "inlineCard", "date", "status", "placeholder":
		return true
	}
	return false
}

func strAttr(attrs map[string]any, key string) string {
	if v, ok := attrs[key].(string); ok {
		return v
	}
	return ""
}

func intAttr(attrs map[string]any, key string, def int) int {
	switch v := attrs[key].(type) {
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func formatDate(v any) string {
	var ms int64
	switch t := v.(type) {
	case string:
		ms, _ = strconv.ParseInt(t, 10, 64)
	case float64:
		ms = int64(t)
	}
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.DateOnly)
}
