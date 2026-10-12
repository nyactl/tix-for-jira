//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/secret"
)

// rawSite calls the Jira REST API directly for setup steps tix itself does
// not offer, such as creating projects.
type rawSite struct {
	cfg   *config.Config
	token string
	me    *jira.User
}

func newRawSite(t *testing.T, profile string) *rawSite {
	t.Helper()
	cfg := testSiteConfig(t, profile)
	token, err := secret.NewKeychain().Get(secret.AccountKey(cfg.Site, cfg.Email))
	if err != nil {
		t.Fatal(err)
	}
	me, err := jira.New(cfg.Site, cfg.Email, token, "integration").Myself(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &rawSite{cfg: cfg, token: token, me: me}
}

// call sends a request and decodes the JSON response; it returns the status.
func (r *rawSite) call(t *testing.T, method, path string, body, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.cfg.Site+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.cfg.Email+":"+r.token)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && out != nil {
		t.Logf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (r *rawSite) hasPermissions(t *testing.T, project string, perms ...string) map[string]bool {
	t.Helper()
	q := "/rest/api/3/mypermissions?permissions=" + strings.Join(perms, ",")
	if project != "" {
		q += "&projectKey=" + project
	}
	var out struct {
		Permissions map[string]struct {
			HavePermission bool `json:"havePermission"`
		} `json:"permissions"`
	}
	if st := r.call(t, http.MethodGet, q, nil, &out); st != 200 {
		t.Fatalf("mypermissions: HTTP %d", st)
	}
	res := map[string]bool{}
	for k, v := range out.Permissions {
		res[k] = v.HavePermission
	}
	return res
}

// TestSetupCompanyManagedProject creates (or completes) a company-managed
// Scrum project on the test site, as an admin account, so estimates,
// versions, components and sprints can be tested. It only runs with
// TIX_JIRA_IT_SETUP=1 and is safe to run again.
func TestSetupCompanyManagedProject(t *testing.T) {
	if os.Getenv("TIX_JIRA_IT_SETUP") != "1" {
		t.Skip("set TIX_JIRA_IT_SETUP=1 to set up the company-managed test project")
	}
	key := os.Getenv("TIX_JIRA_IT_SETUP_PROJECT")
	if key == "" {
		key = "CMP"
	}
	adminProfile := os.Getenv("TIX_JIRA_IT_SETUP_ADMIN_PROFILE")
	if adminProfile == "" {
		adminProfile = "test2"
	}
	admin := newRawSite(t, adminProfile)
	tested := newRawSite(t, "test")
	if admin.cfg.Site != tested.cfg.Site {
		t.Fatalf("profiles %s and test point to different sites", adminProfile)
	}

	if !admin.hasPermissions(t, "", "ADMINISTER")["ADMINISTER"] {
		t.Fatalf("profile %s is not a Jira administrator, so it cannot create projects; nothing was changed", adminProfile)
	}
	t.Logf("profile %s is a Jira administrator", adminProfile)

	var project struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if admin.call(t, http.MethodGet, "/rest/api/3/project/"+key, nil, &project) == 404 {
		var created struct {
			ID json.Number `json:"id"`
		}
		st := admin.call(t, http.MethodPost, "/rest/api/3/project", map[string]any{
			"key":                key,
			"name":               "tix-for-jira company-managed tests",
			"projectTypeKey":     "software",
			"projectTemplateKey": "com.pyxis.greenhopper.jira:gh-simplified-scrum-classic",
			"leadAccountId":      admin.me.AccountID,
			"assigneeType":       "UNASSIGNED",
		}, &created)
		if st != 201 {
			t.Fatalf("creating project %s: HTTP %d", key, st)
		}
		admin.call(t, http.MethodGet, "/rest/api/3/project/"+key, nil, &project)
		t.Logf("created project %s", key)
	} else {
		t.Logf("project %s already exists", key)
	}

	var versions []struct{ Name string }
	admin.call(t, http.MethodGet, "/rest/api/3/project/"+key+"/versions", nil, &versions)
	for _, name := range []string{"1.0", "1.1"} {
		exists := false
		for _, v := range versions {
			exists = exists || v.Name == name
		}
		if !exists {
			if st := admin.call(t, http.MethodPost, "/rest/api/3/version", map[string]any{"name": name, "projectId": project.ID}, &struct{}{}); st != 201 {
				t.Fatalf("creating version %s: HTTP %d", name, st)
			}
			t.Logf("created version %s", name)
		}
	}
	var components []struct{ Name string }
	admin.call(t, http.MethodGet, "/rest/api/3/project/"+key+"/components", nil, &components)
	for _, name := range []string{"Backend", "Frontend"} {
		exists := false
		for _, c := range components {
			exists = exists || c.Name == name
		}
		if !exists {
			if st := admin.call(t, http.MethodPost, "/rest/api/3/component", map[string]any{"name": name, "project": key}, &struct{}{}); st != 201 {
				t.Fatalf("creating component %s: HTTP %d", name, st)
			}
			t.Logf("created component %s", name)
		}
	}

	addFieldsToScreens(t, admin, project.ID, "timetracking", "versions")

	// The tested account needs normal access: browse, create, edit, work on
	// issues and be assignable. Add it to a member role if it lacks them.
	needed := []string{"BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "ASSIGNABLE_USER", "WORK_ON_ISSUES", "TRANSITION_ISSUES", "SCHEDULE_ISSUES"}
	missing := func() []string {
		var m []string
		for p, ok := range tested.hasPermissions(t, key, needed...) {
			if !ok {
				m = append(m, p)
			}
		}
		return m
	}
	if m := missing(); len(m) > 0 {
		var roles map[string]string
		admin.call(t, http.MethodGet, "/rest/api/3/project/"+key+"/role", nil, &roles)
		var roleURL string
		for _, name := range []string{"Member", "Developers", "Administrators"} {
			if u, ok := roles[name]; ok {
				roleURL = u
				break
			}
		}
		if roleURL == "" {
			t.Fatalf("test account lacks %v and no member role was found in %v", m, roles)
		}
		path := roleURL[strings.Index(roleURL, "/rest/"):]
		if st := admin.call(t, http.MethodPost, path, map[string]any{"user": []string{tested.me.AccountID}}, &struct{}{}); st != 200 {
			t.Fatalf("adding the test account to %s: HTTP %d", path, st)
		}
		if m := missing(); len(m) > 0 {
			t.Fatalf("test account still lacks %v in %s", m, key)
		}
		t.Log("added the test account to a member role")
	}
	if tested.hasPermissions(t, "", "ADMINISTER")["ADMINISTER"] {
		t.Log("warning: the test account is a Jira administrator; tests should run as a normal user")
	}

	// Someone else's ticket, for the scope checks.
	var types struct {
		IssueTypes []struct{ ID, Name string } `json:"issueTypes"`
	}
	admin.call(t, http.MethodGet, "/rest/api/3/issue/createmeta/"+key+"/issuetypes", nil, &types)
	var taskID string
	for _, it := range types.IssueTypes {
		if it.Name == "Task" {
			taskID = it.ID
		}
	}
	var found struct {
		Issues []struct{ Key string } `json:"issues"`
	}
	admin.call(t, http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+strings.ReplaceAll(fmt.Sprintf(`project = %s AND summary ~ "someone else" AND assignee = currentUser()`, key), " ", "%20"), nil, &found)
	other := ""
	if len(found.Issues) > 0 {
		other = found.Issues[0].Key
	} else {
		var created struct{ Key string }
		st := admin.call(t, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": map[string]any{
			"project":   map[string]string{"key": key},
			"issuetype": map[string]string{"id": taskID},
			"summary":   "Ticket of someone else (tix-for-jira tests)",
			"assignee":  map[string]string{"accountId": admin.me.AccountID},
		}}, &created)
		if st != 201 {
			t.Fatalf("creating the other person's ticket: HTTP %d", st)
		}
		other = created.Key
	}

	// Check what the tested account can edit on a ticket of its own.
	var mine struct{ Key string }
	if st := tested.call(t, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": map[string]any{
		"project":   map[string]string{"key": key},
		"issuetype": map[string]string{"id": taskID},
		"summary":   "tix-for-jira setup probe",
		"labels":    []string{"tix-for-jira-it"},
	}}, &mine); st != 201 {
		t.Fatalf("the test account cannot create tickets in %s: HTTP %d", key, st)
	}
	closeWhenDone(t, jira.New(tested.cfg.Site, tested.cfg.Email, tested.token, "integration"), mine.Key)
	var meta struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	tested.call(t, http.MethodGet, "/rest/api/3/issue/"+mine.Key+"/editmeta", nil, &meta)
	for _, f := range []string{"timetracking", "fixVersions", "versions", "components", "environment", "parent"} {
		_, ok := meta.Fields[f]
		t.Logf("edit screen has %-12s %v", f, ok)
	}
	var boards struct {
		Values []struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		} `json:"values"`
	}
	tested.call(t, http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId="+key, nil, &boards)
	t.Logf("boards visible to the test account: %+v", boards.Values)
	t.Logf("setup done: project %s, someone else's ticket %s; use TIX_JIRA_IT_PROJECT=%s TIX_JIRA_IT_OTHER=%s", key, other, key, other)
}

// addFieldsToScreens puts fields on every screen the project uses (create,
// edit and view), so they can be set through the API.
func addFieldsToScreens(t *testing.T, admin *rawSite, projectID string, fields ...string) {
	t.Helper()
	var schemes struct {
		Values []struct {
			IssueTypeScreenScheme struct {
				ID string `json:"id"`
			} `json:"issueTypeScreenScheme"`
		} `json:"values"`
	}
	admin.call(t, http.MethodGet, "/rest/api/3/issuetypescreenscheme/project?projectId="+projectID, nil, &schemes)
	if len(schemes.Values) == 0 {
		t.Fatalf("no issue type screen scheme for project %s", projectID)
	}
	var mappings struct {
		Values []struct {
			ScreenSchemeID string `json:"screenSchemeId"`
		} `json:"values"`
	}
	admin.call(t, http.MethodGet, "/rest/api/3/issuetypescreenscheme/mapping?issueTypeScreenSchemeId="+schemes.Values[0].IssueTypeScreenScheme.ID, nil, &mappings)
	screens := map[int64]bool{}
	for _, m := range mappings.Values {
		var ss struct {
			Values []struct {
				Screens map[string]int64 `json:"screens"`
			} `json:"values"`
		}
		admin.call(t, http.MethodGet, "/rest/api/3/screenscheme?id="+m.ScreenSchemeID, nil, &ss)
		for _, v := range ss.Values {
			for _, id := range v.Screens {
				screens[id] = true
			}
		}
	}
	for screen := range screens {
		var tabs []struct {
			ID int64 `json:"id"`
		}
		admin.call(t, http.MethodGet, fmt.Sprintf("/rest/api/3/screens/%d/tabs", screen), nil, &tabs)
		if len(tabs) == 0 {
			continue
		}
		var present []struct {
			ID string `json:"id"`
		}
		admin.call(t, http.MethodGet, fmt.Sprintf("/rest/api/3/screens/%d/tabs/%d/fields", screen, tabs[0].ID), nil, &present)
		have := map[string]bool{}
		for _, f := range present {
			have[f.ID] = true
		}
		for _, f := range fields {
			if have[f] {
				continue
			}
			st := admin.call(t, http.MethodPost, fmt.Sprintf("/rest/api/3/screens/%d/tabs/%d/fields", screen, tabs[0].ID), map[string]string{"fieldId": f}, &struct{}{})
			t.Logf("screen %d: add %s -> HTTP %d", screen, f, st)
		}
	}
}
