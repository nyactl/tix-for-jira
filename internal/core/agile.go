package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

const maxBoards = 200

// Boards lists the boards the user can see, optionally of one project.
func (s *Service) Boards(ctx context.Context, project string) ([]BoardView, error) {
	if project != "" {
		pk, err := jira.NormalizeProjectKey(project)
		if err != nil {
			return nil, err
		}
		project = pk
	}
	bs, err := s.jc.Boards(ctx, project, maxBoards)
	if err != nil {
		return nil, err
	}
	for i := range bs {
		s.boardView(&bs[i])
	}
	// Names are redacted only once every board owner is known.
	out := make([]BoardView, len(bs))
	for i := range bs {
		out[i] = s.boardView(&bs[i])
	}
	return out, nil
}

func (s *Service) boardView(b *jira.Board) BoardView {
	if l := b.Location; l.UserAccountID != "" {
		s.people.ID(l.UserAccountID, l.DisplayName, "")
	}
	return BoardView{ID: b.ID, Name: s.people.Text(b.Name), Type: b.Type, Project: b.Location.ProjectKey}
}

// board resolves a board ID or name.
func (s *Service) board(ctx context.Context, ref string) (*jira.Board, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("name a board (see tix boards)")
	}
	if id, err := strconv.Atoi(ref); err == nil && id > 0 {
		return s.jc.Board(ctx, id)
	}
	bs, err := s.jc.Boards(ctx, "", maxBoards)
	if err != nil {
		return nil, err
	}
	var partial []*jira.Board
	for i := range bs {
		switch {
		case strings.EqualFold(bs[i].Name, ref):
			return &bs[i], nil
		case strings.Contains(strings.ToLower(bs[i].Name), strings.ToLower(ref)):
			partial = append(partial, &bs[i])
		}
	}
	switch len(partial) {
	case 0:
		return nil, fmt.Errorf("no board named %q (see tix boards)", ref)
	case 1:
		return partial[0], nil
	}
	names := make([]string, len(partial))
	for i, b := range partial {
		names[i] = fmt.Sprintf("%s (%d)", s.people.Text(b.Name), b.ID)
	}
	return nil, fmt.Errorf("board %q is ambiguous; use one of: %s", ref, strings.Join(names, ", "))
}

func (s *Service) sprintView(sp *jira.Sprint) SprintView {
	return SprintView{ID: sp.ID, Name: s.people.Text(sp.Name), State: sp.State,
		Start: s.when(sp.StartDate), End: s.when(sp.EndDate), Goal: s.people.Text(sp.Goal)}
}

func (s *Service) sprints(ctx context.Context, b *jira.Board, states string) ([]jira.Sprint, error) {
	noSprints := fmt.Errorf("board %q has no sprints (a Kanban board, or sprints are turned off)", s.people.Text(b.Name))
	if b.Type == "kanban" {
		return nil, noSprints
	}
	sps, err := s.jc.Sprints(ctx, b.ID, states, maxBoards)
	if err != nil && strings.Contains(err.Error(), "does not support sprints") {
		return nil, noSprints
	}
	return sps, err
}

// Sprints lists a board's active and future sprints.
func (s *Service) Sprints(ctx context.Context, boardRef string) ([]SprintView, error) {
	b, err := s.board(ctx, boardRef)
	if err != nil {
		return nil, err
	}
	sps, err := s.sprints(ctx, b, "active,future")
	if err != nil {
		return nil, err
	}
	out := make([]SprintView, len(sps))
	for i := range sps {
		out[i] = s.sprintView(&sps[i])
	}
	return out, nil
}

// ActiveSprints lists the user's tickets in each active sprint of a board.
func (s *Service) ActiveSprints(ctx context.Context, boardRef string, max int) ([]BoardIssues, error) {
	b, err := s.board(ctx, boardRef)
	if err != nil {
		return nil, err
	}
	sps, err := s.sprints(ctx, b, "active")
	if err != nil {
		return nil, err
	}
	if len(sps) == 0 {
		return nil, fmt.Errorf("board %q has no active sprint", s.people.Text(b.Name))
	}
	out := make([]BoardIssues, 0, len(sps))
	for i := range sps {
		issues, total, err := s.jc.SprintIssues(ctx, sps[i].ID, s.scopeFilter(), summaryFields, limit(max))
		if err != nil {
			return nil, err
		}
		found := s.inScope(issues)
		sv := s.sprintView(&sps[i])
		out = append(out, BoardIssues{Board: s.boardView(b), Sprint: &sv, Issues: found, Total: total})
	}
	return out, nil
}

// Backlog lists the user's tickets in a board's backlog.
func (s *Service) Backlog(ctx context.Context, boardRef string, max int) (*BoardIssues, error) {
	b, err := s.board(ctx, boardRef)
	if err != nil {
		return nil, err
	}
	issues, total, err := s.jc.BacklogIssues(ctx, b.ID, s.scopeFilter(), summaryFields, limit(max))
	if err != nil {
		return nil, err
	}
	found := s.inScope(issues)
	return &BoardIssues{Board: s.boardView(b), Issues: found, Total: total}, nil
}

// scopeFilter is the JQL that limits board queries to the privacy scope.
// Results are re-checked by inScope regardless.
func (s *Service) scopeFilter() string {
	if s.Mode() == config.PrivacyOff {
		return ""
	}
	return "assignee = currentUser()"
}

func (s *Service) inScope(issues []jira.Issue) []IssueSummary {
	out := []IssueSummary{}
	for i := range issues {
		if s.people.InScope(issues[i].Fields.Assignee) {
			s.learn(&issues[i])
			out = append(out, s.summary(&issues[i]))
		}
	}
	return out
}

func limit(max int) int {
	if max <= 0 {
		return 50
	}
	return min(max, 200)
}
