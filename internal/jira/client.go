// Package jira is a minimal Jira Cloud REST v3 client covering what
// tix-for-jira needs. It runs with the permissions of the token's user; no
// admin endpoints are used. Authentication, the API gateway, retries and
// error decoding come from approvekit's atlassian package.
package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/nyactl/approvekit/atlassian"
)

// Client talks to one Jira Cloud site as one user.
type Client struct {
	*atlassian.Client
}

// New returns a client for site (already normalised to https://host).
func New(site, email, token, version string, opts ...atlassian.Option) *Client {
	opts = append([]atlassian.Option{atlassian.WithScopeHint(agileScopes)}, opts...)
	return &Client{atlassian.New(atlassian.Jira, site, email, token, "tix-for-jira/"+version, opts...)}
}

// do sends a request and decodes the JSON response into out, if not nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return withScopeHelp(c.Do(ctx, method, path, query, body, out))
}

// send performs a request; the caller closes the body of the response.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	resp, err := c.Send(ctx, method, path, query, body)
	return resp, withScopeHelp(err)
}

func withScopeHelp(err error) error {
	if errors.Is(err, atlassian.ErrMissingScope) {
		return fmt.Errorf("%w; create a token with the scopes listed in the README (Scoped API tokens) and run `tix auth login`", err)
	}
	return err
}

var (
	issueKeyRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
	projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	numericIDRe  = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// NormalizeKey upper-cases and validates an issue key such as PROJ-123.
// Keys end up in URL paths, so anything else is rejected up front.
func NormalizeKey(key string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(key))
	if !issueKeyRe.MatchString(k) {
		return "", fmt.Errorf("invalid issue key %q: expected e.g. PROJ-123", key)
	}
	return k, nil
}

// NormalizeProjectKey upper-cases and validates a project key.
func NormalizeProjectKey(key string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(key))
	if !projectKeyRe.MatchString(k) {
		return "", fmt.Errorf("invalid project key %q", key)
	}
	return k, nil
}

func checkID(id string) error {
	if !numericIDRe.MatchString(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	return nil
}

func issuePath(key string, rest ...string) (string, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return "", err
	}
	return "/rest/api/3/issue/" + k + strings.Join(rest, ""), nil
}
