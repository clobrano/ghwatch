package pr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/model"
)

func TestParse(t *testing.T) {
	ok := map[string]string{
		"https://github.com/Org/Repo/pull/123":            "pr:org/repo#123",
		"https://github.com/org/repo/pull/123/checks":     "pr:org/repo#123",
		"https://github.com/org/repo/pull/123#discussion": "pr:org/repo#123",
		"github.com/org/repo/pull/7":                      "pr:org/repo#7",
		"org/repo#123":                                    "pr:org/repo#123",
		"pr:org/my.repo-x#9":                              "pr:org/my.repo-x#9",
		"  medik8s/node-healthcheck-operator#58 ":         "pr:medik8s/node-healthcheck-operator#58",
	}
	for in, want := range ok {
		got, parsed := Kind{}.Parse(in)
		if !parsed || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, parsed, want)
		}
	}
	for _, in := range []string{"", "org/repo", "org/repo#0", "https://github.com/org/repo/issues/3", "https://gitlab.com/o/r/pull/1", "o/r#x"} {
		if got, parsed := (Kind{}).Parse(in); parsed {
			t.Errorf("Parse(%q) = %q, want rejection", in, got)
		}
	}
}

func TestCheckRunState(t *testing.T) {
	tests := []struct {
		status, conclusion string
		want               model.State
	}{
		{"QUEUED", "", model.Pending},
		{"WAITING", "", model.Pending},
		{"IN_PROGRESS", "", model.Running},
		{"COMPLETED", "SUCCESS", model.Passed},
		{"COMPLETED", "NEUTRAL", model.Passed},
		{"COMPLETED", "FAILURE", model.Failed},
		{"COMPLETED", "TIMED_OUT", model.Failed},
		{"COMPLETED", "ACTION_REQUIRED", model.Failed},
		{"COMPLETED", "SKIPPED", model.Skipped},
		{"COMPLETED", "CANCELLED", model.Cancelled},
		{"COMPLETED", "STALE", model.Cancelled},
	}
	for _, tt := range tests {
		if got := CheckRunState(tt.status, tt.conclusion); got != tt.want {
			t.Errorf("CheckRunState(%s, %s) = %s, want %s", tt.status, tt.conclusion, got, tt.want)
		}
	}
}

func TestStatusState(t *testing.T) {
	tests := []struct {
		state, url string
		want       model.State
	}{
		{"PENDING", "", model.Pending},
		{"PENDING", "https://prow.ci/view/1", model.Running},
		{"EXPECTED", "", model.Pending},
		{"SUCCESS", "x", model.Passed},
		{"FAILURE", "x", model.Failed},
		{"ERROR", "x", model.Failed},
	}
	for _, tt := range tests {
		if got := StatusState(tt.state, tt.url); got != tt.want {
			t.Errorf("StatusState(%s, %q) = %s, want %s", tt.state, tt.url, got, tt.want)
		}
	}
}

const rollup = `[
 {"__typename":"CheckRun","name":"unit","status":"COMPLETED","conclusion":"FAILURE","databaseId":1,
  "startedAt":"2026-09-30T10:00:00Z","completedAt":"2026-09-30T10:05:00Z","detailsUrl":"https://github.com/o/r/actions/runs/11/job/1",
  "isRequired":true,"checkSuite":{"app":{"slug":"github-actions","name":"GitHub Actions"}}},
 {"__typename":"CheckRun","name":"unit","status":"IN_PROGRESS","conclusion":null,"databaseId":2,
  "startedAt":"2026-09-30T10:10:00Z","completedAt":null,"detailsUrl":"https://github.com/o/r/actions/runs/11/job/2",
  "isRequired":true,"checkSuite":{"app":{"slug":"github-actions","name":"GitHub Actions"}}},
 {"__typename":"StatusContext","context":"ci/prow/e2e-aws-ovn","state":"FAILURE","targetUrl":"https://prow.ci.openshift.org/view/gs/1",
  "createdAt":"2026-09-30T10:41:00Z","isRequired":false},
 {"__typename":"StatusContext","context":"tide","state":"PENDING","targetUrl":"","createdAt":"2026-09-30T10:00:00Z","isRequired":false},
 {"__typename":"CheckRun","name":"codecov","status":"COMPLETED","conclusion":"SUCCESS","databaseId":3,
  "checkSuite":{"app":{"slug":"codecov","name":"Codecov"}}}
]`

func TestNormalize(t *testing.T) {
	var nodes []json.RawMessage
	if err := json.Unmarshal([]byte(rollup), &nodes); err != nil {
		t.Fatal(err)
	}
	got := Normalize(nodes)
	if len(got) != 4 {
		t.Fatalf("got %d checks, want 4 (re-run deduplicated): %+v", len(got), got)
	}
	unit := got[0]
	if unit.Name != "unit" || unit.State != model.Running || unit.Source != SourceActions || !unit.Required {
		t.Errorf("unit = %+v, want the latest run, running", unit)
	}
	if unit.CompletedAt != nil {
		t.Errorf("running check has a completion time")
	}
	e2e := got[1]
	if e2e.Source != SourceProw || e2e.State != model.Failed || e2e.CompletedAt == nil || e2e.Required {
		t.Errorf("e2e = %+v", e2e)
	}
	if tide := got[2]; tide.State != model.Pending || tide.Source != SourceProw {
		t.Errorf("tide = %+v", tide)
	}
	if cov := got[3]; cov.Source != "Codecov" || cov.State != model.Passed {
		t.Errorf("codecov = %+v", cov)
	}
}

func TestFetchBatchesOneRequest(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req struct{ Query string }
		json.Unmarshal(body, &req)
		for _, want := range []string{"p0: repository(owner: \"o\", name: \"r\")", "pullRequest(number: 1)", "p1:", "isRequired(pullRequestNumber: 2)", "rateLimit"} {
			if !strings.Contains(req.Query, want) {
				t.Errorf("query lacks %q:\n%s", want, req.Query)
			}
		}
		io.WriteString(w, `{"data":{
		  "p0":{"nameWithOwner":"o/r","pullRequest":{"number":1,"title":"Fix race","url":"https://github.com/o/r/pull/1",
		    "state":"OPEN","merged":false,"headRefName":"fix-race","headRefOid":"abc1234567","author":{"login":"me"},
		    "commits":{"nodes":[{"commit":{"oid":"abc1234567","committedDate":"2026-09-30T09:00:00Z",
		      "statusCheckRollup":{"contexts":{"nodes":`+rollup+`}}}}]}}},
		  "p1":{"nameWithOwner":"o/r","pullRequest":null},
		  "rateLimit":{"limit":5000,"remaining":4990,"resetAt":"2026-09-30T11:00:00Z","cost":1}},
		 "errors":[{"type":"NOT_FOUND","path":["p1","pullRequest"],"message":"Could not resolve to a PullRequest with the number of 2."}]}`)
	}))
	defer srv.Close()
	gh := github.New()
	gh.Endpoint = srv.URL
	gh.TokenFunc = func() (string, error) { return "t", nil }

	items, rl, err := Kind{}.Fetch(context.Background(), gh, []string{"pr:o/r#1", "pr:o/r#2"})
	if err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("made %d requests, want 1", n)
	}
	if rl == nil || rl.Remaining != 4990 || rl.Low() {
		t.Errorf("rate limit = %+v", rl)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	pr := items[0]
	if pr.Title != "Fix race" || pr.Author != "me" || pr.HeadSHA != "abc1234567" || pr.Lifecycle != model.Open || len(pr.Checks) != 4 {
		t.Errorf("item = %+v", pr)
	}
	// unit (required) is running; the failing Prow job is optional.
	if pr.State != model.Running {
		t.Errorf("state = %s, want running", pr.State)
	}
	if items[1].Error == "" || !strings.Contains(items[1].Error, "Could not resolve") {
		t.Errorf("missing PR error = %q", items[1].Error)
	}
}

func TestRetest(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(body))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	gh := github.New()
	gh.API = srv.URL
	gh.TokenFunc = func() (string, error) { return "t", nil }
	it := model.Item{ID: "pr:o/r#5", Checks: []model.Check{
		{Name: "unit", Source: SourceActions, State: model.Failed, URL: "https://github.com/o/r/actions/runs/42/job/7"},
		{Name: "lint", Source: SourceActions, State: model.Failed, URL: "https://github.com/o/r/actions/runs/42/job/8"},
		{Name: "e2e", Source: SourceProw, State: model.Failed, URL: "https://prow/x"},
		{Name: "ok", Source: SourceProw, State: model.Passed},
	}}
	info, err := Kind{}.Retest(context.Background(), gh, it)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /repos/o/r/actions/runs/42/rerun-failed-jobs ",
		`POST /repos/o/r/issues/5/comments {"body":"/retest"}`,
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if !strings.Contains(info, "/retest") {
		t.Errorf("info = %q", info)
	}
}
