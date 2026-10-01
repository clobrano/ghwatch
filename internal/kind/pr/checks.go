package pr

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

// Check sources.
const (
	SourceActions = "Actions"
	SourceProw    = "Prow"
	SourceStatus  = "Status"
)

// Provider turns one raw statusCheckRollup context into a check. Each
// provider handles one GraphQL __typename; adding a CI system that
// reports differently means adding a provider, not touching the poll loop.
type Provider interface {
	Typename() string
	Normalize(raw json.RawMessage) (check model.Check, runID int64, err error)
}

// Providers are the check providers used by Normalize.
var Providers = []Provider{checkRunProvider{}, statusProvider{}}

// Normalize converts the rollup contexts of a head commit into checks,
// keeping only the latest run of each check when a check was re-run.
// prURL is the pull request's URL: a check run then links to its page on
// GitHub, as the pull request page does, and keeps the app's own link in
// DetailsURL. Commit statuses link to their target URL.
func Normalize(nodes []json.RawMessage, prURL string) []model.Check {
	byType := map[string]Provider{}
	for _, p := range Providers {
		byType[p.Typename()] = p
	}
	var (
		out    []model.Check
		latest = map[string]int{}   // key -> index in out
		runIDs = map[string]int64{} // key -> run id of the kept check
	)
	for _, raw := range nodes {
		var head struct {
			Typename string `json:"__typename"`
		}
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		p := byType[head.Typename]
		if p == nil {
			continue
		}
		c, runID, err := p.Normalize(raw)
		if err != nil || c.Name == "" {
			continue
		}
		if head.Typename == "CheckRun" && prURL != "" {
			c.URL, c.DetailsURL = checkRunLink(c, runID, prURL)
		}
		key := c.Key()
		if i, ok := latest[key]; ok {
			if runID > runIDs[key] {
				out[i] = c
				runIDs[key] = runID
			}
			continue
		}
		latest[key] = len(out)
		runIDs[key] = runID
		out = append(out, c)
	}
	return out
}

// checkRunLink returns the link GitHub's pull request page uses for a check
// run, and the app's own link when that differs. A GitHub Actions job links
// to its job page with "?pr=<number>"; a check run from another app (such
// as Konflux) links to its page on GitHub, which links on to the app.
func checkRunLink(c model.Check, runID int64, prURL string) (url, details string) {
	if c.Source == SourceActions && strings.Contains(c.URL, "/actions/runs/") {
		number := prURL[strings.LastIndexByte(prURL, '/')+1:]
		sep := "?"
		if strings.Contains(c.URL, "?") {
			sep = "&"
		}
		if strings.Contains(c.URL, "pr=") {
			return c.URL, ""
		}
		return c.URL + sep + "pr=" + number, ""
	}
	if runID <= 0 {
		return c.URL, ""
	}
	return fmt.Sprintf("%s/checks?check_run_id=%d", prURL, runID), c.URL
}

type checkRunProvider struct{}

func (checkRunProvider) Typename() string { return "CheckRun" }

func (checkRunProvider) Normalize(raw json.RawMessage) (model.Check, int64, error) {
	var n struct {
		Name        string     `json:"name"`
		Status      string     `json:"status"`
		Conclusion  string     `json:"conclusion"`
		StartedAt   *time.Time `json:"startedAt"`
		CompletedAt *time.Time `json:"completedAt"`
		DetailsURL  string     `json:"detailsUrl"`
		DatabaseID  int64      `json:"databaseId"`
		IsRequired  bool       `json:"isRequired"`
		CheckSuite  *struct {
			App *struct {
				Slug string `json:"slug"`
				Name string `json:"name"`
			} `json:"app"`
		} `json:"checkSuite"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return model.Check{}, 0, err
	}
	c := model.Check{
		Name: n.Name, State: CheckRunState(n.Status, n.Conclusion), Required: n.IsRequired,
		StartedAt: n.StartedAt, CompletedAt: n.CompletedAt, URL: n.DetailsURL, Source: SourceActions,
	}
	if s := n.CheckSuite; s != nil && s.App != nil && s.App.Slug != "github-actions" {
		c.Source = s.App.Name
		if strings.Contains(strings.ToLower(s.App.Slug), "prow") {
			c.Source = SourceProw
		}
	}
	if !c.State.Done() {
		c.CompletedAt = nil
	}
	return c, n.DatabaseID, nil
}

// CheckRunState maps a check-run status and conclusion to a check state.
func CheckRunState(status, conclusion string) model.State {
	switch strings.ToUpper(status) {
	case "IN_PROGRESS":
		return model.Running
	case "COMPLETED":
	default: // QUEUED, WAITING, PENDING, REQUESTED
		return model.Pending
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL":
		return model.Passed
	case "SKIPPED":
		return model.Skipped
	case "CANCELLED", "STALE":
		return model.Cancelled
	}
	// FAILURE, TIMED_OUT, ACTION_REQUIRED, STARTUP_FAILURE
	return model.Failed
}

type statusProvider struct{}

func (statusProvider) Typename() string { return "StatusContext" }

func (statusProvider) Normalize(raw json.RawMessage) (model.Check, int64, error) {
	var n struct {
		Context    string     `json:"context"`
		State      string     `json:"state"`
		TargetURL  string     `json:"targetUrl"`
		CreatedAt  *time.Time `json:"createdAt"`
		IsRequired bool       `json:"isRequired"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return model.Check{}, 0, err
	}
	c := model.Check{
		Name: n.Context, State: StatusState(n.State, n.TargetURL), Required: n.IsRequired,
		URL: n.TargetURL, Source: StatusSource(n.Context, n.TargetURL),
	}
	// A status carries only the time of its latest state: the start while
	// it runs, the end once it finished. The daemon carries the start over
	// from earlier polls.
	if c.State.Done() {
		c.CompletedAt = n.CreatedAt
	} else if c.State == model.Running {
		c.StartedAt = n.CreatedAt
	}
	return c, 0, nil
}

// StatusState maps a commit-status state to a check state. A pending
// status with a job link is running; without one it has not started.
func StatusState(state, targetURL string) model.State {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return model.Passed
	case "FAILURE", "ERROR":
		return model.Failed
	}
	if targetURL != "" {
		return model.Running
	}
	return model.Pending
}

// StatusSource names the CI system behind a commit status.
func StatusSource(context, targetURL string) string {
	u := strings.ToLower(targetURL)
	if strings.Contains(u, "prow") || strings.HasPrefix(context, "ci/prow/") || context == "tide" {
		return SourceProw
	}
	return SourceStatus
}
