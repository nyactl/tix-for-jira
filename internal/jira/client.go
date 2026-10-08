// Package jira is a minimal Jira Cloud REST v3 client covering what
// tix-jira needs. It runs with the permissions of the token's user; no
// admin endpoints are used.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxJSONBytes = 16 << 20
	maxRetries   = 3
	maxRetryWait = 10 * time.Second
)

var (
	ErrUnauthorized = errors.New("credentials rejected by Jira: the API token may be expired or revoked; create a new one at https://id.atlassian.com/manage-profile/security/api-tokens and run `tix-jira auth login`")
	ErrNotFound     = errors.New("not found, or you do not have permission to see it")
)

// APIError is a non-2xx response that is not mapped to a sentinel error.
type APIError struct {
	Status   int
	Messages []string
}

func (e *APIError) Error() string {
	prefix := fmt.Sprintf("Jira returned HTTP %d", e.Status)
	if e.Status == http.StatusForbidden {
		prefix = "permission denied by Jira"
	}
	if len(e.Messages) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(e.Messages, "; ")
}

// Client talks to one Jira Cloud site as one user.
type Client struct {
	site      string
	auth      string
	userAgent string
	http      *http.Client
	sleep     func(context.Context, time.Duration) error
}

// Option customises a Client.
type Option func(*Client)

// WithTransport sets the HTTP transport, e.g. to trust a test server.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) { c.http.Transport = rt }
}

// New returns a client for site (already normalised to https://host).
func New(site, email, token, version string, opts ...Option) *Client {
	c := &Client{
		site:      strings.TrimRight(site, "/"),
		auth:      "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token)),
		userAgent: "tix-jira/" + version,
		http: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: sameOrigin,
		},
		sleep: sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Site returns the base URL, e.g. https://team.atlassian.net.
func (c *Client) Site() string { return c.site }

// sameOrigin refuses redirects that change scheme or host. Attachments are
// fetched with redirect=false, so no legitimate call needs to leave the site.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("refusing redirect to %s://%s", req.URL.Scheme, req.URL.Host)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	u := c.site + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// send performs the request, retrying on 429 and 503 as advised by
// Retry-After. The caller must close the body of a successful response.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := c.newRequest(ctx, method, path, query, body)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
		if retryable && attempt < maxRetries {
			wait := retryAfter(resp.Header.Get("Retry-After"), attempt)
			_ = resp.Body.Close()
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
}

func retryAfter(h string, attempt int) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, maxRetryWait)
	}
	return min(time.Duration(1<<attempt)*time.Second, maxRetryWait)
}

func decodeError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	var body struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	e := &APIError{Status: resp.StatusCode, Messages: body.ErrorMessages}
	keys := make([]string, 0, len(body.Errors))
	for k := range body.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e.Messages = append(e.Messages, k+": "+body.Errors[k])
	}
	return e
}

// do sends a request and decodes a JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	resp, err := c.send(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxJSONBytes))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxJSONBytes {
		return fmt.Errorf("response from %s exceeds %d bytes", path, maxJSONBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return nil
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
