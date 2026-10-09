//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
)

// TestCloseLeftoverTickets closes every open ticket labelled tix-jira-it,
// e.g. from runs that were interrupted. It only runs with
// TIX_JIRA_IT_CLEANUP=1.
func TestCloseLeftoverTickets(t *testing.T) {
	if os.Getenv("TIX_JIRA_IT_CLEANUP") != "1" {
		t.Skip("set TIX_JIRA_IT_CLEANUP=1 to close leftover test tickets")
	}
	s := setup(t)
	issues, err := s.jc.Search(context.Background(), "project = "+s.project+` AND (labels = tix-jira-it OR summary ~ "tix-jira integration") AND statusCategory != Done ORDER BY key`, []string{"summary"}, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range issues {
		closeWhenDone(t, s.jc, i.Key)
	}
	t.Logf("closing %d leftover test tickets", len(issues))
}
