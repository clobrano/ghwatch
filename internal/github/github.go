// Package github provides token lookup and a small GraphQL/REST client
// over net/http.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Token returns the GitHub token from $GITHUB_TOKEN, $GH_TOKEN or
// `gh auth token`. ghwatch never writes it to disk.
func Token() (string, error) {
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no GitHub token: set $GITHUB_TOKEN or log in with `gh auth login` (%v)", err)
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", errors.New("no GitHub token: `gh auth token` printed nothing")
	}
	return t, nil
}

// RateLimit is the GraphQL rate limit reported with the last response.
type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
	Cost      int       `json:"cost"`
}

// Low reports whether the remaining budget is under 5% of the limit.
func (r RateLimit) Low() bool { return r.Limit > 0 && r.Remaining*20 < r.Limit }

// Client talks to the GitHub API.
type Client struct {
	HTTP     *http.Client
	Endpoint string // GraphQL endpoint
	API      string // REST base URL
	// TokenFunc returns the token for each request, so an expired token
	// is picked up again from gh without restarting the daemon.
	TokenFunc func() (string, error)
}

// New returns a client for github.com.
func New() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		Endpoint:  "https://api.github.com/graphql",
		API:       "https://api.github.com",
		TokenFunc: Token,
	}
}

// Error is an API-level failure.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("github: %d %s", e.Status, e.Message)
	}
	return "github: " + e.Message
}

// Auth reports whether the error is an authentication failure.
func (e *Error) Auth() bool { return e.Status == http.StatusUnauthorized }

// RateLimited reports whether the error is a rate-limit rejection.
func (e *Error) RateLimited() bool {
	return e.Status == http.StatusForbidden && strings.Contains(strings.ToLower(e.Message), "rate limit") ||
		e.Status == http.StatusTooManyRequests
}

// GraphQLError is one entry of a GraphQL "errors" array.
type GraphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// Response is a decoded GraphQL response.
type Response struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors"`
}

// GraphQL runs a query and returns the raw response. Partial errors (for
// example one missing pull request in a batch) are returned in
// Response.Errors rather than as err, so the caller can keep the rest.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any) (*Response, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := c.do(ctx, http.MethodPost, c.Endpoint, body, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || string(resp.Data) == "null" {
		if len(resp.Errors) > 0 {
			return nil, &Error{Message: resp.Errors[0].Message}
		}
		return nil, &Error{Message: "empty response"}
	}
	return &resp, nil
}

// REST sends a REST request; out may be nil.
func (c *Client) REST(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	return c.do(ctx, method, c.API+path, body, out)
}

func (c *Client) do(ctx context.Context, method, url string, body []byte, out any) error {
	token, err := c.TokenFunc()
	if err != nil {
		return &Error{Status: http.StatusUnauthorized, Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghwatch")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = http.StatusText(res.StatusCode)
		}
		return &Error{Status: res.StatusCode, Message: e.Message}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
