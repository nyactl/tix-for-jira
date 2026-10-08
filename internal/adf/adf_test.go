package adf

import (
	"encoding/json"
	"strings"
	"testing"
)

func doc(content string) json.RawMessage {
	return json.RawMessage(`{"type":"doc","version":1,"content":[` + content + `]}`)
}

func TestToMarkdown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{"null", json.RawMessage(`null`), ""},
		{"empty", nil, ""},
		{"plain string field", json.RawMessage(`"legacy text"`), "legacy text"},
		{"paragraphs", doc(`{"type":"paragraph","content":[{"type":"text","text":"one"}]},{"type":"paragraph","content":[{"type":"text","text":"two"}]}`), "one\n\ntwo"},
		{"heading", doc(`{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Steps"}]}`), "### Steps"},
		{"marks", doc(`{"type":"paragraph","content":[
			{"type":"text","text":"b","marks":[{"type":"strong"}]},{"type":"text","text":" "},
			{"type":"text","text":"i","marks":[{"type":"em"}]},{"type":"text","text":" "},
			{"type":"text","text":"x()","marks":[{"type":"code"}]},{"type":"text","text":" "},
			{"type":"text","text":"gone","marks":[{"type":"strike"}]},{"type":"text","text":" "},
			{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}]}`),
			"**b** *i* `x()` ~~gone~~ [site](https://example.com)"},
		{"hard break", doc(`{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"hardBreak"},{"type":"text","text":"b"}]}`), "a\nb"},
		{"nested lists", doc(`{"type":"bulletList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},
				{"type":"orderedList","attrs":{"order":3},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"sub"}]}]}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]}`),
			"- one\n\n  3. sub\n- two"},
		{"code block", doc(`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1\ny := 2"}]}`), "```go\nx := 1\ny := 2\n```"},
		{"quote and rule", doc(`{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"q"}]}]},{"type":"rule"}`), "> q\n\n---"},
		{"panel", doc(`{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"careful"}]}]}`), "> **Warning:** careful"},
		{"table", doc(`{"type":"table","content":[
			{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"A"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"B"}]}]}]},
			{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1|2"}]}]},{"type":"tableCell","content":[]}]}]}`),
			"| A | B |\n| --- | --- |\n| 1\\|2 |  |"},
		{"mention uses callback", doc(`{"type":"paragraph","content":[{"type":"text","text":"ask "},{"type":"mention","attrs":{"id":"acc-1","text":"@Real Name"}}]}`), "ask @[Person A]"},
		{"inline nodes", doc(`{"type":"paragraph","content":[
			{"type":"emoji","attrs":{"shortName":":smile:","text":"S"}},{"type":"text","text":" "},
			{"type":"status","attrs":{"text":"BLOCKED"}},{"type":"text","text":" "},
			{"type":"date","attrs":{"timestamp":"1791504000000"}},{"type":"text","text":" "},
			{"type":"inlineCard","attrs":{"url":"https://example.com/x"}}]}`),
			"S [BLOCKED] 2026-10-09 <https://example.com/x>"},
		{"media", doc(`{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"m1","type":"file","alt":"shot.png"}}]}`), "[attachment: shot.png]"},
		{"tasks", doc(`{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"done"}]},{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"open"}]}]}`), "- [x] done\n- [ ] open"},
		{"unknown node keeps text", doc(`{"type":"futureThing","content":[{"type":"text","text":"still here"}]}`), "still here"},
	}
	mention := func(id, fallback string) string {
		if id == "acc-1" {
			return "Person A"
		}
		return fallback
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ToMarkdown(tt.in, mention)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestToMarkdownRejectsInvalid(t *testing.T) {
	t.Parallel()
	if _, err := ToMarkdown(json.RawMessage(`{"type":`), nil); err == nil {
		t.Error("expected error")
	}
}

func resolver(labels map[string][2]string) Resolver {
	return func(label string) (string, string, bool) {
		v, ok := labels[label]
		return v[0], v[1], ok
	}
}

func mustFromMarkdown(t *testing.T, md string, r Resolver) string {
	t.Helper()
	out, err := FromMarkdown(md, r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestFromMarkdown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `{"content":[],"type":"doc","version":1}`},
		{"paragraph with marks", "a **b** *c* `d` ~~e~~ [f](https://x.test)",
			`{"content":[{"type":"paragraph","content":[{"type":"text","text":"a "},{"type":"text","text":"b","marks":[{"type":"strong"}]},{"type":"text","text":" "},{"type":"text","text":"c","marks":[{"type":"em"}]},{"type":"text","text":" "},{"type":"text","text":"d","marks":[{"type":"code"}]},{"type":"text","text":" "},{"type":"text","text":"e","marks":[{"type":"strike"}]},{"type":"text","text":" "},{"type":"text","text":"f","marks":[{"type":"link","attrs":{"href":"https://x.test"}}]}]}],"type":"doc","version":1}`},
		{"line breaks kept", "one\ntwo",
			`{"content":[{"type":"paragraph","content":[{"type":"text","text":"one"},{"type":"hardBreak"},{"type":"text","text":"two"}]}],"type":"doc","version":1}`},
		{"heading and rule", "## Title\n\n---",
			`{"content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},{"type":"rule"}],"type":"doc","version":1}`},
		{"code block", "```go\nx := 1\n```",
			`{"content":[{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]}],"type":"doc","version":1}`},
		{"ordered list start", "3. a\n4. b",
			`{"content":[{"type":"orderedList","attrs":{"order":3},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]}],"type":"doc","version":1}`},
		{"heading inside quote becomes bold paragraph", "> # Hi",
			`{"content":[{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"Hi","marks":[{"type":"strong"}]}]}]}],"type":"doc","version":1}`},
		{"mention", "thanks @[Person A]!",
			`{"content":[{"type":"paragraph","content":[{"type":"text","text":"thanks "},{"type":"mention","attrs":{"id":"acc-1","text":"@Alice Real"}},{"type":"text","text":"!"}]}],"type":"doc","version":1}`},
		{"mention syntax in code stays text", "`@[Person A]`",
			`{"content":[{"type":"paragraph","content":[{"type":"text","text":"@[Person A]","marks":[{"type":"code"}]}]}],"type":"doc","version":1}`},
		{"table", "| A | B |\n|---|---|\n| 1 | 2 |",
			`{"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"A"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"B"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"2"}]}]}]}]}],"type":"doc","version":1}`},
	}
	r := resolver(map[string][2]string{"Person A": {"acc-1", "Alice Real"}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Normalise key order by round-tripping through a map.
			var want, got any
			_ = json.Unmarshal([]byte(tt.want), &want)
			_ = json.Unmarshal([]byte(mustFromMarkdown(t, tt.in, r)), &got)
			wb, _ := json.Marshal(want)
			gb, _ := json.Marshal(got)
			if string(wb) != string(gb) {
				t.Errorf("got:\n%s\nwant:\n%s", gb, wb)
			}
		})
	}
}

func TestFromMarkdownUnknownMention(t *testing.T) {
	t.Parallel()
	_, err := FromMarkdown("hi @[Person Z]", resolver(nil))
	if err == nil || !strings.Contains(err.Error(), "@[Person Z]") {
		t.Errorf("err = %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	in := "## Plan\n\n- **first** step\n- second with `code`\n\n> note\n\n```sh\nmake test\n```\n\nsee [docs](https://x.test) and @[Person A]"
	adfDoc, err := FromMarkdown(in, resolver(map[string][2]string{"Person A": {"acc-1", "Alice"}}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ToMarkdown(adfDoc, func(id, fb string) string {
		if id == "acc-1" {
			return "Person A"
		}
		return fb
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip changed text:\n%s\n---\n%s", out, in)
	}
}
