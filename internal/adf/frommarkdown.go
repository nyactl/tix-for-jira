package adf

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Resolver maps a mention label written as @[label] to an account ID and
// the display text Jira should store with it.
type Resolver func(label string) (accountID, display string, ok bool)

var mentionRe = regexp.MustCompile(`@\[([^\[\]\n]{1,100})\]`)

var md = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.Table, extension.TaskList))

// FromMarkdown converts Markdown to an ADF document. Supported: paragraphs,
// headings, bullet and ordered lists, fenced and indented code, block
// quotes, rules, tables, bold, italic, strikethrough, inline code, links
// and @[label] mentions. Anything else is kept as plain text.
func FromMarkdown(markdown string, resolve Resolver) (json.RawMessage, error) {
	src := []byte(markdown)
	c := &converter{src: src, resolve: resolve}
	root := md.Parser().Parse(text.NewReader(src))
	content := c.children(root)
	if c.err != nil {
		return nil, c.err
	}
	if content == nil {
		content = []Node{}
	}
	return json.Marshal(map[string]any{"type": "doc", "version": 1, "content": content})
}

type converter struct {
	src     []byte
	resolve Resolver
	err     error
}

func (c *converter) children(n ast.Node) []Node {
	var out []Node
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		out = append(out, c.block(ch)...)
	}
	return out
}

func (c *converter) block(n ast.Node) []Node {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return []Node{{Type: "paragraph", Content: c.inlineBlock(n)}}
	case *ast.Heading:
		return []Node{{Type: "heading", Attrs: map[string]any{"level": n.Level}, Content: c.inlineBlock(n)}}
	case *ast.ThematicBreak:
		return []Node{{Type: "rule"}}
	case *ast.FencedCodeBlock:
		node := codeBlock(c.lines(n))
		if lang := string(n.Language(c.src)); lang != "" {
			node.Attrs = map[string]any{"language": lang}
		}
		return []Node{node}
	case *ast.CodeBlock:
		return []Node{codeBlock(c.lines(n))}
	case *ast.HTMLBlock:
		return []Node{{Type: "paragraph", Content: textNodes(strings.TrimRight(c.lines(n), "\n"), nil)}}
	case *ast.Blockquote:
		var inner []Node
		for _, b := range c.children(n) {
			if b.Type == "heading" {
				b = Node{Type: "paragraph", Content: withMark(b.Content, Mark{Type: "strong"})}
			}
			inner = append(inner, b)
		}
		return []Node{{Type: "blockquote", Content: inner}}
	case *ast.List:
		list := Node{Type: "bulletList"}
		if n.IsOrdered() {
			list.Type = "orderedList"
			if n.Start > 1 {
				list.Attrs = map[string]any{"order": n.Start}
			}
		}
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			content := c.children(item)
			if len(content) == 0 {
				content = []Node{{Type: "paragraph"}}
			}
			list.Content = append(list.Content, Node{Type: "listItem", Content: content})
		}
		return []Node{list}
	case *extast.Table:
		table := Node{Type: "table"}
		for row := n.FirstChild(); row != nil; row = row.NextSibling() {
			cellType := "tableCell"
			if _, ok := row.(*extast.TableHeader); ok {
				cellType = "tableHeader"
			}
			r := Node{Type: "tableRow"}
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				r.Content = append(r.Content, Node{Type: cellType, Content: []Node{{Type: "paragraph", Content: c.inlineBlock(cell)}}})
			}
			table.Content = append(table.Content, r)
		}
		return []Node{table}
	default:
		return c.children(n)
	}
}

func (c *converter) lines(n ast.Node) string {
	var b strings.Builder
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		b.Write(seg.Value(c.src))
	}
	return b.String()
}

func codeBlock(code string) Node {
	code = strings.TrimSuffix(code, "\n")
	n := Node{Type: "codeBlock"}
	if code != "" {
		n.Content = []Node{{Type: "text", Text: code}}
	}
	return n
}

// inlineBlock converts the inline children of a block and resolves mentions.
func (c *converter) inlineBlock(n ast.Node) []Node {
	nodes := c.inlines(n, nil)
	nodes = mergeText(nodes)
	nodes = c.mentions(nodes)
	for len(nodes) > 0 && nodes[len(nodes)-1].Type == "hardBreak" {
		nodes = nodes[:len(nodes)-1]
	}
	return nodes
}

func (c *converter) inlines(parent ast.Node, marks []Mark) []Node {
	var out []Node
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		switch n := n.(type) {
		case *ast.Text:
			out = append(out, textNodes(string(n.Value(c.src)), marks)...)
			if n.SoftLineBreak() || n.HardLineBreak() {
				out = append(out, Node{Type: "hardBreak"})
			}
		case *ast.String:
			out = append(out, textNodes(string(n.Value), marks)...)
		case *ast.CodeSpan:
			var b strings.Builder
			for t := n.FirstChild(); t != nil; t = t.NextSibling() {
				if tx, ok := t.(*ast.Text); ok {
					b.Write(tx.Value(c.src))
				}
			}
			codeMarks := []Mark{{Type: "code"}}
			for _, m := range marks {
				if m.Type == "link" {
					codeMarks = append(codeMarks, m)
				}
			}
			out = append(out, textNodes(b.String(), codeMarks)...)
		case *ast.Emphasis:
			mark := Mark{Type: "em"}
			if n.Level >= 2 {
				mark.Type = "strong"
			}
			out = append(out, c.inlines(n, addMark(marks, mark))...)
		case *extast.Strikethrough:
			out = append(out, c.inlines(n, addMark(marks, Mark{Type: "strike"}))...)
		case *ast.Link:
			out = append(out, c.inlines(n, addMark(marks, linkMark(string(n.Destination))))...)
		case *ast.AutoLink:
			url := string(n.URL(c.src))
			out = append(out, textNodes(string(n.Label(c.src)), addMark(marks, linkMark(url)))...)
		case *ast.Image:
			alt := c.inlines(n, addMark(marks, linkMark(string(n.Destination))))
			if len(alt) == 0 {
				alt = textNodes(string(n.Destination), addMark(marks, linkMark(string(n.Destination))))
			}
			out = append(out, alt...)
		case *ast.RawHTML:
			var b strings.Builder
			for i := range n.Segments.Len() {
				seg := n.Segments.At(i)
				b.Write(seg.Value(c.src))
			}
			out = append(out, textNodes(b.String(), marks)...)
		case *extast.TaskCheckBox:
			box := "[ ] "
			if n.IsChecked {
				box = "[x] "
			}
			out = append(out, textNodes(box, marks)...)
		default:
			out = append(out, c.inlines(n, marks)...)
		}
	}
	return out
}

// mentions splits @[label] out of unformatted text into mention nodes.
func (c *converter) mentions(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		if n.Type != "text" || hasMark(n.Marks, "code") || !strings.Contains(n.Text, "@[") {
			out = append(out, n)
			continue
		}
		rest := n.Text
		for {
			loc := mentionRe.FindStringSubmatchIndex(rest)
			if loc == nil {
				break
			}
			out = append(out, textNodes(rest[:loc[0]], n.Marks)...)
			label := rest[loc[2]:loc[3]]
			var accountID, display string
			ok := false
			if c.resolve != nil {
				accountID, display, ok = c.resolve(label)
			}
			if !ok {
				if c.err == nil {
					c.err = fmt.Errorf("cannot mention @[%s]: only people already shown in this session can be mentioned", label)
				}
				out = append(out, textNodes(rest[loc[0]:loc[1]], n.Marks)...)
			} else {
				out = append(out, Node{Type: "mention", Attrs: map[string]any{"id": accountID, "text": "@" + display}})
			}
			rest = rest[loc[1]:]
		}
		out = append(out, textNodes(rest, n.Marks)...)
	}
	return out
}

func textNodes(s string, marks []Mark) []Node {
	if s == "" {
		return nil
	}
	return []Node{{Type: "text", Text: s, Marks: append([]Mark(nil), marks...)}}
}

func mergeText(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		if last := len(out) - 1; last >= 0 && n.Type == "text" && out[last].Type == "text" && reflect.DeepEqual(out[last].Marks, n.Marks) {
			out[last].Text += n.Text
			continue
		}
		out = append(out, n)
	}
	return out
}

func addMark(marks []Mark, m Mark) []Mark {
	if hasMark(marks, m.Type) {
		return marks
	}
	return append(append([]Mark(nil), marks...), m)
}

func withMark(nodes []Node, m Mark) []Node {
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		if n.Type == "text" && !hasMark(n.Marks, "code") {
			n.Marks = addMark(n.Marks, m)
		}
		out[i] = n
	}
	return out
}

func hasMark(marks []Mark, typ string) bool {
	for _, m := range marks {
		if m.Type == typ {
			return true
		}
	}
	return false
}

func linkMark(href string) Mark {
	return Mark{Type: "link", Attrs: map[string]any{"href": href}}
}
