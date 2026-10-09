// Package jiratest provides an in-memory fake of the Jira Cloud REST v3
// endpoints tix-jira uses. It models several users, so scope and redaction
// can be tested with other people's tickets. The JQL support is limited to
// what tix-jira generates.
package jiratest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type User struct {
	AccountID, DisplayName, Email, Token string
}

type Comment struct {
	ID, Author, Created string
	Body                json.RawMessage
}

type Change struct {
	Author, Created                        string
	Field, FieldType, From, FromS, To, ToS string
}

type Worklog struct {
	ID, Author, Started string
	Seconds             int
	Comment             json.RawMessage
}

type Attachment struct {
	ID, Filename, Author, MimeType, Created string
	Data                                    []byte
}

type Link struct {
	ID, Type, Outward, Inward string // "Outward <outward phrase> Inward"
}

type Issue struct {
	Key, Project, Type, Summary, Status, Priority string
	Assignee, Reporter                            string
	Description                                   json.RawMessage
	Labels                                        []string
	DueDate, Created, Updated                     string
	Parent                                        string
	Custom                                        map[string]any
	Comments                                      []Comment
	Changes                                       []Change
	Worklogs                                      []Worklog
	Attachments                                   []Attachment
}

// Fake is the fake Jira site.
type Fake struct {
	t   testing.TB
	mu  sync.Mutex
	srv *httptest.Server

	Users  map[string]*User // accountID -> user
	Issues map[string]*Issue
	Links  []Link
	JQL    []string // every JQL query received
	// UnscopedSearch makes search ignore "assignee = currentUser()", as a
	// crafted query could, to test that callers re-check every result.
	UnscopedSearch bool
	Requests       []string // "METHOD path" of every request
	nextID         int
}

var statuses = map[string]struct{ id, cat string }{
	"To Do":       {"10000", "new"},
	"In Progress": {"3", "indeterminate"},
	"Done":        {"10001", "done"},
}

var transitions = []struct{ id, name, to string }{
	{"11", "Back to To Do", "To Do"},
	{"21", "Start Progress", "In Progress"},
	{"31", "Done", "Done"},
}

var issueTypes = []struct {
	id, name string
	subtask  bool
}{
	{"10001", "Task", false},
	{"10002", "Bug", false},
	{"10003", "Subtask", true},
}

var linkTypes = []struct{ id, name, outward, inward string }{
	{"10000", "Blocks", "blocks", "is blocked by"},
	{"10003", "Relates", "relates to", "relates to"},
}

// Custom field definitions: Story Points (number), Team (select),
// Reviewer (user), Notes (rich text).
var customFields = []struct{ id, name, typ, custom string }{
	{"customfield_10016", "Story Points", "number", "com.atlassian.jira.plugin.system.customfieldtypes:float"},
	{"customfield_10050", "Team", "option", "com.atlassian.jira.plugin.system.customfieldtypes:select"},
	{"customfield_10060", "Reviewer", "user", "com.atlassian.jira.plugin.system.customfieldtypes:userpicker"},
	{"customfield_10070", "Notes", "string", "com.atlassian.jira.plugin.system.customfieldtypes:textarea"},
}

// New starts a fake site with users Me (me@example.com, token "tok-me"),
// Alice and Bob, and no issues.
func New(t testing.TB) *Fake {
	f := &Fake{
		t:      t,
		Users:  map[string]*User{},
		Issues: map[string]*Issue{},
		nextID: 100,
	}
	f.AddUser(User{AccountID: "acc-me", DisplayName: "Max Mustermann", Email: "me@example.com", Token: "tok-me"})
	f.AddUser(User{AccountID: "acc-alice", DisplayName: "Alice Smith", Email: "alice@example.com", Token: "tok-alice"})
	f.AddUser(User{AccountID: "acc-bob", DisplayName: "Bob Jones", Email: "bob@example.com", Token: "tok-bob"})
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the https base URL of the fake site.
func (f *Fake) URL() string { return f.srv.URL }

// Transport trusts the fake site's test certificate.
func (f *Fake) Transport() http.RoundTripper { return f.srv.Client().Transport }

func (f *Fake) AddUser(u User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Users[u.AccountID] = &u
}

// AddIssue stores an issue, filling defaults.
func (f *Fake) AddIssue(i Issue) *Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i.Project == "" {
		i.Project = strings.Split(i.Key, "-")[0]
	}
	if i.Type == "" {
		i.Type = "Task"
	}
	if i.Status == "" {
		i.Status = "To Do"
	}
	if i.Created == "" {
		i.Created = "2026-10-01T09:00:00.000+0000"
	}
	if i.Updated == "" {
		i.Updated = i.Created
	}
	if i.Custom == nil {
		i.Custom = map[string]any{}
	}
	f.Issues[i.Key] = &i
	return &i
}

// Doc wraps text as a one-paragraph ADF document; mentions are written as
// {acc-id} and become mention nodes.
func Doc(text string) json.RawMessage {
	var content []map[string]any
	re := regexp.MustCompile(`\{(acc-[a-z]+)\}`)
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > last {
			content = append(content, map[string]any{"type": "text", "text": text[last:m[0]]})
		}
		content = append(content, map[string]any{"type": "mention", "attrs": map[string]any{"id": text[m[2]:m[3]], "text": "@someone"}})
		last = m[1]
	}
	if last < len(text) {
		content = append(content, map[string]any{"type": "text", "text": text[last:]})
	}
	b, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph", "content": content}}})
	return b
}

func (f *Fake) id() string {
	f.nextID++
	return strconv.Itoa(f.nextID)
}

func (f *Fake) userJSON(id string) any {
	u, ok := f.Users[id]
	if !ok || id == "" {
		return nil
	}
	return map[string]any{
		"accountId":    u.AccountID,
		"displayName":  u.DisplayName,
		"emailAddress": u.Email,
		"active":       true,
		"avatarUrls":   map[string]string{"48x48": "https://avatar.example/" + u.AccountID},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{msg}, "errors": map[string]string{}})
}

func (f *Fake) caller(r *http.Request) *User {
	h := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
	raw, err := base64.StdEncoding.DecodeString(h)
	if err != nil {
		return nil
	}
	email, token, _ := strings.Cut(string(raw), ":")
	for _, u := range f.Users {
		if u.Email == email && u.Token == token {
			return u
		}
	}
	return nil
}

var (
	reIssue       = regexp.MustCompile(`^/rest/api/3/issue/([A-Z][A-Z0-9_]*-[0-9]+)(/[a-z]+)?$`)
	reAttachment  = regexp.MustCompile(`^/rest/api/3/attachment/content/([0-9]+)$`)
	reCreateTypes = regexp.MustCompile(`^/rest/api/3/issue/createmeta/([A-Z0-9_]+)/issuetypes(?:/([0-9]+))?$`)
)

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.Path)
	me := f.caller(r)
	if me == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body map[string]any
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
	}
	p := r.URL.Path
	switch {
	case p == "/rest/api/3/myself":
		writeJSON(w, 200, f.userJSON(me.AccountID))
	case p == "/rest/api/3/search/jql":
		f.search(w, r, me)
	case p == "/rest/api/3/field":
		f.fieldList(w)
	case p == "/rest/api/3/issueLinkType":
		var out []any
		for _, lt := range linkTypes {
			out = append(out, map[string]string{"id": lt.id, "name": lt.name, "outward": lt.outward, "inward": lt.inward})
		}
		writeJSON(w, 200, map[string]any{"issueLinkTypes": out})
	case p == "/rest/api/3/issueLink" && r.Method == http.MethodPost:
		f.createLink(w, body)
	case p == "/rest/api/3/project/search":
		writeJSON(w, 200, map[string]any{"values": []any{map[string]string{"id": "1", "key": "PROJ", "name": "Project"}}, "total": 1, "isLast": true})
	case p == "/rest/api/3/issue" && r.Method == http.MethodPost:
		f.createIssue(w, body, me)
	case reCreateTypes.MatchString(p):
		f.createMeta(w, reCreateTypes.FindStringSubmatch(p))
	case reAttachment.MatchString(p):
		f.attachment(w, r, reAttachment.FindStringSubmatch(p)[1])
	case reIssue.MatchString(p):
		m := reIssue.FindStringSubmatch(p)
		issue, ok := f.Issues[m[1]]
		if !ok {
			errJSON(w, 404, "Issue does not exist or you do not have permission to see it.")
			return
		}
		f.issueRoute(w, r, issue, m[2], body, me)
	default:
		errJSON(w, 404, "no route for "+r.Method+" "+p)
	}
}

func (f *Fake) search(w http.ResponseWriter, r *http.Request, me *User) {
	q := r.URL.Query()
	jql := q.Get("jql")
	f.JQL = append(f.JQL, jql)
	var keys []string
	for k, i := range f.Issues {
		if !f.UnscopedSearch && strings.Contains(jql, "assignee = currentUser()") && i.Assignee != me.AccountID {
			continue
		}
		if strings.Contains(jql, "statusCategory != Done") && statuses[i.Status].cat == "done" {
			continue
		}
		if m := regexp.MustCompile(`project = "?([A-Z0-9_]+)"?`).FindStringSubmatch(jql); m != nil && i.Project != m[1] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	start, _ := strconv.Atoi(q.Get("nextPageToken"))
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 50
	}
	end := min(start+size, len(keys))
	var issues []any
	for _, k := range keys[start:end] {
		issues = append(issues, f.issueJSON(f.Issues[k], false))
	}
	resp := map[string]any{"issues": issues, "isLast": end >= len(keys)}
	if end < len(keys) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, 200, resp)
}

func (f *Fake) related(key string) any {
	i, ok := f.Issues[key]
	if !ok {
		return map[string]any{"key": key}
	}
	st := statuses[i.Status]
	return map[string]any{"id": "1" + strings.Split(key, "-")[1], "key": key, "fields": map[string]any{
		"summary":   i.Summary,
		"status":    map[string]any{"name": i.Status, "statusCategory": map[string]string{"key": st.cat}},
		"issuetype": map[string]any{"name": i.Type},
	}}
}

func (f *Fake) issueJSON(i *Issue, names bool) map[string]any {
	st := statuses[i.Status]
	fields := map[string]any{
		"summary":     i.Summary,
		"status":      map[string]any{"id": st.id, "name": i.Status, "statusCategory": map[string]string{"key": st.cat, "name": i.Status}},
		"issuetype":   map[string]any{"name": i.Type, "subtask": i.Type == "Subtask"},
		"project":     map[string]any{"key": i.Project, "name": "Project " + i.Project},
		"assignee":    f.userJSON(i.Assignee),
		"reporter":    f.userJSON(i.Reporter),
		"created":     i.Created,
		"updated":     i.Updated,
		"labels":      append([]string{}, i.Labels...),
		"description": i.Description,
	}
	if i.Priority != "" {
		fields["priority"] = map[string]string{"name": i.Priority}
	}
	if i.DueDate != "" {
		fields["duedate"] = i.DueDate
	}
	if i.Parent != "" {
		fields["parent"] = f.related(i.Parent)
	}
	var subtasks []any
	for _, other := range f.sortedIssues() {
		if other.Parent == i.Key {
			subtasks = append(subtasks, f.related(other.Key))
		}
	}
	fields["subtasks"] = subtasks
	var links []any
	for _, l := range f.Links {
		lt := linkTypeByName(l.Type)
		typ := map[string]string{"name": lt.name, "inward": lt.inward, "outward": lt.outward}
		switch i.Key {
		case l.Outward:
			links = append(links, map[string]any{"id": l.ID, "type": typ, "outwardIssue": f.related(l.Inward)})
		case l.Inward:
			links = append(links, map[string]any{"id": l.ID, "type": typ, "inwardIssue": f.related(l.Outward)})
		}
	}
	fields["issuelinks"] = links
	var atts []any
	for _, a := range i.Attachments {
		atts = append(atts, map[string]any{"id": a.ID, "filename": a.Filename, "author": f.userJSON(a.Author), "created": a.Created, "size": len(a.Data), "mimeType": a.MimeType, "content": f.srv.URL + "/rest/api/3/attachment/content/" + a.ID})
	}
	fields["attachment"] = atts
	for id, v := range i.Custom {
		if s, ok := v.(string); ok && strings.HasPrefix(s, "acc-") {
			fields[id] = f.userJSON(s)
			continue
		}
		fields[id] = v
	}
	out := map[string]any{"id": "1" + strings.Split(i.Key, "-")[1], "key": i.Key, "fields": fields}
	if names {
		n := map[string]string{"summary": "Summary", "status": "Status", "description": "Description"}
		for _, cf := range customFields {
			n[cf.id] = cf.name
		}
		out["names"] = n
	}
	return out
}

func (f *Fake) sortedIssues() []*Issue {
	keys := make([]string, 0, len(f.Issues))
	for k := range f.Issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*Issue, len(keys))
	for i, k := range keys {
		out[i] = f.Issues[k]
	}
	return out
}

func linkTypeByName(name string) struct{ id, name, outward, inward string } {
	for _, lt := range linkTypes {
		if strings.EqualFold(lt.name, name) {
			return lt
		}
	}
	return linkTypes[0]
}

func (f *Fake) issueRoute(w http.ResponseWriter, r *http.Request, i *Issue, sub string, body map[string]any, me *User) {
	switch sub + " " + r.Method {
	case " GET":
		writeJSON(w, 200, f.issueJSON(i, r.URL.Query().Get("expand") == "names"))
	case " PUT":
		f.edit(w, i, body)
	case "/assignee PUT":
		i.Assignee, _ = body["accountId"].(string)
		w.WriteHeader(204)
	case "/comment GET":
		var cs []any
		for _, c := range i.Comments {
			cs = append(cs, map[string]any{"id": c.ID, "author": f.userJSON(c.Author), "body": c.Body, "created": c.Created, "updated": c.Created})
		}
		writeJSON(w, 200, paged(r, "comments", cs))
	case "/comment POST":
		raw, _ := json.Marshal(body["body"])
		c := Comment{ID: f.id(), Author: me.AccountID, Body: raw, Created: "2026-10-08T10:00:00.000+0000"}
		i.Comments = append(i.Comments, c)
		writeJSON(w, 201, map[string]any{"id": c.ID, "author": f.userJSON(c.Author), "body": c.Body, "created": c.Created})
	case "/changelog GET":
		var vs []any
		for n, c := range i.Changes {
			vs = append(vs, map[string]any{"id": strconv.Itoa(n + 1), "author": f.userJSON(c.Author), "created": c.Created, "items": []any{map[string]any{
				"field": c.Field, "fieldtype": c.FieldType, "from": c.From, "fromString": c.FromS, "to": c.To, "toString": c.ToS}}})
		}
		writeJSON(w, 200, paged(r, "values", vs))
	case "/worklog GET":
		var ws []any
		for _, wl := range i.Worklogs {
			ws = append(ws, map[string]any{"id": wl.ID, "author": f.userJSON(wl.Author), "comment": wl.Comment, "started": wl.Started, "timeSpent": fmt.Sprintf("%dm", wl.Seconds/60), "timeSpentSeconds": wl.Seconds})
		}
		writeJSON(w, 200, paged(r, "worklogs", ws))
	case "/worklog POST":
		raw, _ := json.Marshal(body["comment"])
		secs, _ := body["timeSpentSeconds"].(float64)
		started, _ := body["started"].(string)
		wl := Worklog{ID: f.id(), Author: me.AccountID, Started: started, Seconds: int(secs), Comment: raw}
		i.Worklogs = append(i.Worklogs, wl)
		writeJSON(w, 201, map[string]any{"id": wl.ID, "author": f.userJSON(me.AccountID), "started": started, "timeSpentSeconds": wl.Seconds})
	case "/transitions GET":
		var ts []any
		for _, t := range transitions {
			st := statuses[t.to]
			ts = append(ts, map[string]any{"id": t.id, "name": t.name, "to": map[string]any{"name": t.to, "statusCategory": map[string]string{"key": st.cat}}})
		}
		writeJSON(w, 200, map[string]any{"transitions": ts})
	case "/transitions POST":
		tr, _ := body["transition"].(map[string]any)
		for _, t := range transitions {
			if t.id == tr["id"] {
				// Like Jira without a transition screen, comments sent along
				// with a transition are silently ignored.
				i.Status = t.to
				w.WriteHeader(204)
				return
			}
		}
		errJSON(w, 400, "Transition id is not valid")
	case "/editmeta GET":
		writeJSON(w, 200, map[string]any{"fields": editMeta()})
	default:
		errJSON(w, 404, "no route")
	}
}

func paged(r *http.Request, key string, items []any) map[string]any {
	start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
	size, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
	if size <= 0 {
		size = 50
	}
	start = min(start, len(items))
	end := min(start+size, len(items))
	return map[string]any{key: items[start:end], "startAt": start, "maxResults": size, "total": len(items), "isLast": end >= len(items)}
}

func editMeta() map[string]any {
	m := map[string]any{
		"summary":     map[string]any{"name": "Summary", "key": "summary", "schema": map[string]string{"type": "string", "system": "summary"}, "operations": []string{"set"}},
		"description": map[string]any{"name": "Description", "key": "description", "schema": map[string]string{"type": "string", "system": "description"}, "operations": []string{"set"}},
		"labels":      map[string]any{"name": "Labels", "key": "labels", "schema": map[string]string{"type": "array", "items": "string", "system": "labels"}, "operations": []string{"add", "set", "remove"}},
		"duedate":     map[string]any{"name": "Due date", "key": "duedate", "schema": map[string]string{"type": "date", "system": "duedate"}, "operations": []string{"set"}},
		"priority": map[string]any{"name": "Priority", "key": "priority", "schema": map[string]string{"type": "priority", "system": "priority"}, "operations": []string{"set"},
			"allowedValues": []any{map[string]string{"id": "2", "name": "High"}, map[string]string{"id": "3", "name": "Medium"}, map[string]string{"id": "4", "name": "Low"}}},
	}
	for _, cf := range customFields {
		meta := map[string]any{"name": cf.name, "key": cf.id, "schema": map[string]string{"type": cf.typ, "custom": cf.custom}, "operations": []string{"set"}}
		if cf.typ == "option" {
			meta["allowedValues"] = []any{map[string]string{"id": "1", "value": "Alpha"}, map[string]string{"id": "2", "value": "Beta"}}
		}
		m[cf.id] = meta
	}
	return m
}

func (f *Fake) edit(w http.ResponseWriter, i *Issue, body map[string]any) {
	fields, _ := body["fields"].(map[string]any)
	for k, v := range fields {
		switch k {
		case "summary":
			i.Summary, _ = v.(string)
		case "description":
			i.Description, _ = json.Marshal(v)
		case "labels":
			i.Labels = nil
			for _, l := range v.([]any) {
				i.Labels = append(i.Labels, l.(string))
			}
		case "duedate":
			i.DueDate, _ = v.(string)
		case "priority":
			i.Priority, _ = v.(map[string]any)["name"].(string)
		default:
			if !strings.HasPrefix(k, "customfield_") {
				errJSON(w, 400, "Field '"+k+"' cannot be set.")
				return
			}
			i.Custom[k] = v
		}
	}
	w.WriteHeader(204)
}

func (f *Fake) fieldList(w http.ResponseWriter) {
	out := []any{
		map[string]any{"id": "summary", "name": "Summary", "custom": false, "schema": map[string]string{"type": "string", "system": "summary"}},
		map[string]any{"id": "assignee", "name": "Assignee", "custom": false, "schema": map[string]string{"type": "user", "system": "assignee"}},
	}
	for _, cf := range customFields {
		out = append(out, map[string]any{"id": cf.id, "name": cf.name, "custom": true, "schema": map[string]string{"type": cf.typ, "custom": cf.custom}})
	}
	writeJSON(w, 200, out)
}

func (f *Fake) createMeta(w http.ResponseWriter, m []string) {
	if m[1] != "PROJ" {
		errJSON(w, 404, "project not found")
		return
	}
	if m[2] == "" {
		var types []any
		for _, t := range issueTypes {
			types = append(types, map[string]any{"id": t.id, "name": t.name, "subtask": t.subtask})
		}
		writeJSON(w, 200, map[string]any{"issueTypes": types, "total": len(types), "startAt": 0, "maxResults": 50})
		return
	}
	fields := []any{
		map[string]any{"fieldId": "summary", "name": "Summary", "required": true, "schema": map[string]string{"type": "string", "system": "summary"}},
		map[string]any{"fieldId": "description", "name": "Description", "required": false, "schema": map[string]string{"type": "string", "system": "description"}},
		map[string]any{"fieldId": "labels", "name": "Labels", "required": false, "schema": map[string]string{"type": "array", "items": "string", "system": "labels"}},
		map[string]any{"fieldId": "assignee", "name": "Assignee", "required": false, "schema": map[string]string{"type": "user", "system": "assignee"}},
		map[string]any{"fieldId": "priority", "name": "Priority", "required": false, "schema": map[string]string{"type": "priority", "system": "priority"},
			"allowedValues": []any{map[string]string{"id": "2", "name": "High"}, map[string]string{"id": "3", "name": "Medium"}}},
	}
	if m[2] == "10003" {
		fields = append(fields, map[string]any{"fieldId": "parent", "name": "Parent", "required": true, "schema": map[string]string{"type": "issuelink", "system": "parent"}})
	}
	writeJSON(w, 200, map[string]any{"fields": fields, "total": len(fields), "startAt": 0, "maxResults": 50})
}

func (f *Fake) createIssue(w http.ResponseWriter, body map[string]any, me *User) {
	fields, _ := body["fields"].(map[string]any)
	proj, _ := fields["project"].(map[string]any)["key"].(string)
	typeID, _ := fields["issuetype"].(map[string]any)["id"].(string)
	typeName := ""
	for _, t := range issueTypes {
		if t.id == typeID {
			typeName = t.name
		}
	}
	if proj != "PROJ" || typeName == "" {
		errJSON(w, 400, "invalid project or issue type")
		return
	}
	n := 1
	for k := range f.Issues {
		if strings.HasPrefix(k, proj+"-") {
			if v, _ := strconv.Atoi(strings.TrimPrefix(k, proj+"-")); v >= n {
				n = v + 1
			}
		}
	}
	i := &Issue{Key: fmt.Sprintf("%s-%d", proj, n), Project: proj, Type: typeName, Status: "To Do", Reporter: me.AccountID, Custom: map[string]any{},
		Created: "2026-10-08T10:00:00.000+0000", Updated: "2026-10-08T10:00:00.000+0000"}
	i.Summary, _ = fields["summary"].(string)
	if d, ok := fields["description"]; ok {
		i.Description, _ = json.Marshal(d)
	}
	if a, ok := fields["assignee"].(map[string]any); ok {
		i.Assignee, _ = a["accountId"].(string)
	}
	if p, ok := fields["parent"].(map[string]any); ok {
		i.Parent, _ = p["key"].(string)
	}
	if ls, ok := fields["labels"].([]any); ok {
		for _, l := range ls {
			i.Labels = append(i.Labels, l.(string))
		}
	}
	f.Issues[i.Key] = i
	writeJSON(w, 201, map[string]string{"id": "1" + strconv.Itoa(n), "key": i.Key})
}

func (f *Fake) createLink(w http.ResponseWriter, body map[string]any) {
	// Like Jira Cloud, the create endpoint takes the subject of the outward
	// phrase as inwardIssue: {inward: A, outward: B, type: Blocks} is "A blocks B".
	typ, _ := body["type"].(map[string]any)["name"].(string)
	out, _ := body["inwardIssue"].(map[string]any)["key"].(string)
	in, _ := body["outwardIssue"].(map[string]any)["key"].(string)
	if f.Issues[out] == nil || f.Issues[in] == nil {
		errJSON(w, 404, "issue not found")
		return
	}
	f.Links = append(f.Links, Link{ID: f.id(), Type: linkTypeByName(typ).name, Outward: out, Inward: in})
	w.WriteHeader(201)
}

func (f *Fake) attachment(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.Query().Get("redirect") != "false" {
		http.Redirect(w, r, "https://media.example/"+id, http.StatusSeeOther)
		return
	}
	for _, i := range f.Issues {
		for _, a := range i.Attachments {
			if a.ID == id {
				w.Header().Set("Content-Type", a.MimeType)
				_, _ = w.Write(a.Data)
				return
			}
		}
	}
	errJSON(w, 404, "attachment not found")
}
