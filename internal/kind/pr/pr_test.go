package pr

import (
	"context"
	"encoding/json"
	"fmt"
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
	got := Normalize(nodes, "")
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

func TestMergeQueueEntry(t *testing.T) {
	if q, _ := buildQuery([]string{"pr:o/r#1"}); !strings.Contains(q, "mergeQueueEntry { state position enqueuedAt estimatedTimeToMerge mergeQueue { url } }") {
		t.Errorf("query does not ask for the merge queue entry:\n%s", q)
	}
	decode := func(js string) model.Item {
		var p prNode
		if err := json.Unmarshal([]byte(js), &p); err != nil {
			t.Fatal(err)
		}
		return p.toItem("pr:o/r#1", "o/r")
	}
	it := decode(`{"number":1,"state":"OPEN","headRefOid":"abc","baseRefName":"main",
	  "mergeQueueEntry":{"state":"AWAITING_CHECKS","position":2,"enqueuedAt":"2026-09-30T10:00:00Z","estimatedTimeToMerge":480},
	  "commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":{"contexts":{"nodes":[
	    {"__typename":"CheckRun","name":"unit","status":"COMPLETED","conclusion":"SUCCESS","databaseId":1,"checkSuite":{"app":{"slug":"github-actions"}}}]}}}}]}}`)
	q := it.MergeQueue
	if q == nil || q.State != "awaiting_checks" || q.Position != 2 || q.ETASeconds != 480 || q.EnqueuedAt.IsZero() {
		t.Fatalf("merge queue = %+v", q)
	}
	if it.State != model.Queued {
		t.Errorf("state = %s, want queued", it.State)
	}
	if q.URL != "https://github.com/o/r/queue/main" {
		t.Errorf("queue URL = %q, want the one built from the base branch", q.URL)
	}
	it = decode(`{"number":1,"state":"OPEN","headRefOid":"abc","baseRefName":"main",
	  "mergeQueueEntry":{"state":"QUEUED","position":1,"mergeQueue":{"url":"https://github.com/o/r/queue/release-4.18"}}}`)
	if it.MergeQueue == nil || it.MergeQueue.URL != "https://github.com/o/r/queue/release-4.18" {
		t.Errorf("queue URL from GitHub: %+v", it.MergeQueue)
	}

	it = decode(`{"number":1,"state":"OPEN","headRefOid":"abc","mergeQueueEntry":null}`)
	if it.MergeQueue != nil {
		t.Errorf("not queued, got %+v", it.MergeQueue)
	}
	it = decode(`{"number":1,"state":"MERGED","merged":true,"headRefOid":"abc","mergeQueueEntry":{"state":"MERGEABLE","position":1}}`)
	if it.MergeQueue != nil || it.State != model.Merged {
		t.Errorf("merged PR: queue %+v, state %s", it.MergeQueue, it.State)
	}
}

func TestCheckRunLinksToGitHub(t *testing.T) {
	var nodes []json.RawMessage
	json.Unmarshal([]byte(`[
	 {"__typename":"CheckRun","name":"Konflux kflux-prd-rh02 / osac-operator-on-pull-request","status":"COMPLETED","conclusion":"FAILURE",
	  "databaseId":109857379366,"detailsUrl":"https://konflux-ui.apps.kflux-prd-rh02.0fk9.p1.openshiftapps.com/ns/osac-tenant/pipelinerun/osac-operator-on-pull-request-86zfs",
	  "checkSuite":{"app":{"slug":"red-hat-konflux","name":"Red Hat Konflux"}}},
	 {"__typename":"CheckRun","name":"unit","status":"COMPLETED","conclusion":"FAILURE","databaseId":7,
	  "detailsUrl":"https://github.com/osac-project/osac/actions/runs/42/job/7","checkSuite":{"app":{"slug":"github-actions"}}},
	 {"__typename":"StatusContext","context":"ci/prow/e2e","state":"FAILURE","targetUrl":"https://prow.ci/view/1"}
	]`), &nodes)
	got := Normalize(nodes, "https://github.com/osac-project/osac/pull/1228")

	konflux := got[0]
	if konflux.URL != "https://github.com/osac-project/osac/pull/1228/checks?check_run_id=109857379366" {
		t.Errorf("Konflux URL = %q, want the GitHub check page", konflux.URL)
	}
	if !strings.HasPrefix(konflux.DetailsURL, "https://konflux-ui.apps.") {
		t.Errorf("Konflux details URL = %q", konflux.DetailsURL)
	}
	// An Actions job links to its job page, as on GitHub's PR page.
	if got[1].URL != "https://github.com/osac-project/osac/actions/runs/42/job/7?pr=1228" || got[1].DetailsURL != "" {
		t.Errorf("Actions check = %+v", got[1])
	}
	// A commit status has no GitHub check page: it keeps its target URL.
	if got[2].URL != "https://prow.ci/view/1" || got[2].DetailsURL != "" {
		t.Errorf("status = %+v", got[2])
	}

	// Re-running failed Actions jobs still finds the run, via the details URL.
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	gh := github.New()
	gh.API = srv.URL
	gh.TokenFunc = func() (string, error) { return "t", nil }
	if _, err := (Kind{}).Retest(context.Background(), gh, model.Item{ID: "pr:osac-project/osac#1228", Checks: got[1:2]}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "POST /repos/osac-project/osac/actions/runs/42/rerun-failed-jobs" {
		t.Errorf("calls = %v", calls)
	}
}

func TestLabels(t *testing.T) {
	if q, _ := buildQuery([]string{"pr:o/r#1"}); !strings.Contains(q, "labels(first: 20) { nodes { name color } }") {
		t.Errorf("query does not ask for labels:\n%s", q)
	}
	var p prNode
	json.Unmarshal([]byte(`{"number":1,"state":"OPEN","headRefOid":"abc",
	  "labels":{"nodes":[{"name":"lgtm","color":"0e8a16"},{"name":"needs-rebase","color":"b60205"}]}}`), &p)
	it := p.toItem("pr:o/r#1", "o/r")
	if len(it.Labels) != 2 || it.Labels[0] != (model.Label{Name: "lgtm", Color: "0e8a16"}) || it.Labels[1].Name != "needs-rebase" {
		t.Errorf("labels = %+v", it.Labels)
	}
}

func TestActionsJobLink(t *testing.T) {
	// osac-project/osac#1332: GitHub links this job to its Actions page.
	var nodes []json.RawMessage
	json.Unmarshal([]byte(`[
	 {"__typename":"CheckRun","name":"VMaaS full install test","status":"COMPLETED","conclusion":"FAILURE","databaseId":110291378942,
	  "detailsUrl":"https://github.com/osac-project/osac/actions/runs/36790862965/job/110291378942","checkSuite":{"app":{"slug":"github-actions"}}},
	 {"__typename":"CheckRun","name":"already","status":"COMPLETED","conclusion":"SUCCESS","databaseId":2,
	  "detailsUrl":"https://github.com/o/r/actions/runs/1/job/2?pr=1332","checkSuite":{"app":{"slug":"github-actions"}}}
	]`), &nodes)
	got := Normalize(nodes, "https://github.com/osac-project/osac/pull/1332")
	if got[0].URL != "https://github.com/osac-project/osac/actions/runs/36790862965/job/110291378942?pr=1332" {
		t.Errorf("job link = %q", got[0].URL)
	}
	if got[1].URL != "https://github.com/o/r/actions/runs/1/job/2?pr=1332" {
		t.Errorf("link with pr already set = %q", got[1].URL)
	}
}

func TestThreads(t *testing.T) {
	var p prNode
	err := json.Unmarshal([]byte(`{"number":1,"state":"OPEN","headRefOid":"abc","url":"https://github.com/o/r/pull/1",
	  "comments":{"totalCount":31,"nodes":[
	    {"id":"IC1","author":{"login":"openshift-ci-robot"},"bodyText":"\n  /retest\nmore","createdAt":"2026-01-01T10:05:00Z","url":"u/IC1"},
	    {"id":"IC2","author":null,"bodyText":"from a deleted account","createdAt":"2026-01-01T10:01:00Z","url":"u/IC2","viewerDidAuthor":false}]},
	  "reviews":{"nodes":[
	    {"id":"R1","author":{"login":"bob"},"bodyText":"","state":"APPROVED","submittedAt":"2026-01-01T10:03:00Z","url":"u/R1"},
	    {"id":"R2","author":{"login":"bob"},"bodyText":"","state":"COMMENTED","submittedAt":"2026-01-01T10:04:00Z","url":"u/R2"},
	    {"id":"R3","author":{"login":"me"},"bodyText":"draft","state":"PENDING","submittedAt":null,"url":"u/R3","viewerDidAuthor":true}]},
	  "reviewThreads":{"nodes":[
	    {"id":"T1","isResolved":false,"isOutdated":true,"path":"pkg/a.go","line":null,"originalLine":42,
	     "comments":{"totalCount":7,"nodes":[{"id":"RC1","author":{"login":"me"},"bodyText":"done","createdAt":"2026-01-01T10:06:00Z","url":"u/RC1","viewerDidAuthor":true}]}}]}}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	it := p.toItem("pr:o/r#1", "o/r")
	if len(it.Threads) != 2 {
		t.Fatalf("threads = %+v", it.Threads)
	}
	conv := it.Threads[0]
	var got []string
	for _, c := range conv.Comments {
		got = append(got, c.ID+"/"+c.Author+"/"+c.Body)
	}
	// Oldest first; the empty "commented" review and the pending one are left out.
	want := "IC2/ghost/from a deleted account R1/bob/approved IC1/openshift-ci-robot//retest\nmore"
	if strings.Join(got, " ") != want || conv.Total != 32 || conv.Name() != "conversation" {
		t.Errorf("conversation = %v (total %d), want %s", got, conv.Total, want)
	}
	if conv.Comments[2].Summary() != "/retest" {
		t.Errorf("summary = %q", conv.Comments[2].Summary())
	}
	if long := text(strings.Repeat("x", model.BodyLimit+10)); len([]rune(long)) != model.BodyLimit {
		t.Errorf("long comment kept %d characters", len([]rune(long)))
	}
	if conv.Comments[1].Review != "approved" {
		t.Errorf("review state = %q", conv.Comments[1].Review)
	}
	th := it.Threads[1]
	if th.Name() != "pkg/a.go:42" || !th.Outdated || th.Resolved || th.Total != 7 || !th.Comments[0].Mine || th.Link() != "u/RC1" {
		t.Errorf("thread = %+v", th)
	}
}

func TestReplyAndResolve(t *testing.T) {
	var calls []string
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(body))
		if r.URL.Path == "/graphql" {
			if fail {
				w.Write([]byte(`{"data":{"resolveReviewThread":null},"errors":[{"message":"Resource not accessible by integration"}]}`))
				return
			}
			w.Write([]byte(`{"data":{"x":{}}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	gh := github.New()
	gh.API, gh.Endpoint = srv.URL, srv.URL+"/graphql"
	gh.TokenFunc = func() (string, error) { return "t", nil }
	ctx := context.Background()
	it := model.Item{ID: "pr:o/r#5"}

	if err := (Kind{}).Reply(ctx, gh, it, model.ConversationID, "LGTM"); err != nil {
		t.Fatal(err)
	}
	if err := (Kind{}).Reply(ctx, gh, it, "PRRT_1", "fixed\nin abc"); err != nil {
		t.Fatal(err)
	}
	if err := (Kind{}).Resolve(ctx, gh, it, "PRRT_1", true); err != nil {
		t.Fatal(err)
	}
	if err := (Kind{}).Resolve(ctx, gh, it, "PRRT_1", false); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || calls[0] != `POST /repos/o/r/issues/5/comments {"body":"LGTM"}` ||
		!strings.Contains(calls[1], "addPullRequestReviewThreadReply") || !strings.Contains(calls[1], `"variables":{"body":"fixed\nin abc","thread":"PRRT_1"}`) ||
		!strings.Contains(calls[2], "resolveReviewThread(input: {threadId: $thread})") || !strings.Contains(calls[3], "unresolveReviewThread") {
		t.Errorf("calls =\n%s", strings.Join(calls, "\n"))
	}
	if err := (Kind{}).Resolve(ctx, gh, it, model.ConversationID, true); err == nil {
		t.Error("resolving the conversation should fail")
	}
	fail = true
	if err := (Kind{}).Resolve(ctx, gh, it, "PRRT_1", true); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Errorf("GraphQL error not reported: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		query = req.Query
		w.Write([]byte(`{"data":{
		  "s0":{"nodes":[{"number":12,"repository":{"nameWithOwner":"Org/Repo"}},{"number":3,"repository":{"nameWithOwner":"o/x"}}]},
		  "s1":{"nodes":[{"number":3,"repository":{"nameWithOwner":"o/x"}},{}]}}}`))
	}))
	defer srv.Close()
	gh := github.New()
	gh.Endpoint = srv.URL
	gh.TokenFunc = func() (string, error) { return "t", nil }

	got, err := Kind{}.Discover(context.Background(), gh, []string{model.AddedReview, model.AddedAssigned})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `s0: search(query: "is:pr is:open archived:false user-review-requested:@me", type: ISSUE, first: 50)`) ||
		!strings.Contains(query, `s1: search(query: "is:pr is:open archived:false assignee:@me"`) {
		t.Errorf("query =\n%s", query)
	}
	// IDs are canonical; a PR found by both searches has both reasons;
	// non-PR nodes (issues come back empty) are skipped.
	if len(got) != 2 || strings.Join(got["pr:org/repo#12"], ",") != "review requested" ||
		strings.Join(got["pr:o/x#3"], ",") != "review requested,assigned" {
		t.Errorf("found = %v", got)
	}
	if _, err := (Kind{}).Discover(context.Background(), gh, []string{"authored"}); err == nil {
		t.Error("unknown reason accepted")
	}
}

func TestViewerReview(t *testing.T) {
	parse := func(js string) model.Item {
		var p prNode
		if err := json.Unmarshal([]byte(js), &p); err != nil {
			t.Fatal(err)
		}
		return p.toItem("pr:o/r#1", "o/r")
	}
	it := parse(`{"number":1,"state":"OPEN","headRefOid":"a",
	  "viewerLatestReview":{"state":"CHANGES_REQUESTED","submittedAt":"2026-01-01T10:00:00Z"},
	  "viewerLatestReviewRequest":{"requestedReviewer":{"__typename":"User"}}}`)
	if r := it.Review; !r.Requested || r.Team != "" || r.State != "changes_requested" || r.At.IsZero() || !r.Reviewed() {
		t.Errorf("review = %+v", r)
	}
	it = parse(`{"number":1,"state":"OPEN","headRefOid":"a",
	  "viewerLatestReview":{"state":"PENDING","submittedAt":null},
	  "viewerLatestReviewRequest":{"requestedReviewer":{"__typename":"Team","name":"nhc-reviewers"}}}`)
	if r := it.Review; !r.Requested || r.Team != "nhc-reviewers" || r.State != "pending" || r.Reviewed() {
		t.Errorf("review = %+v", r)
	}
	if r := parse(`{"number":1,"state":"OPEN","headRefOid":"a","viewerLatestReview":null,"viewerLatestReviewRequest":null}`).Review; r != (model.MyReview{}) {
		t.Errorf("review = %+v", r)
	}
	if !strings.Contains(prSelection, "viewerLatestReview { state submittedAt }") || !strings.Contains(prSelection, "viewerLatestReviewRequest") {
		t.Error("the query does not ask for the viewer's review")
	}
}

func TestThreadHunk(t *testing.T) {
	var hunk []string
	hunk = append(hunk, "@@ -1,20 +1,21 @@")
	for i := 1; i <= 20; i++ {
		hunk = append(hunk, fmt.Sprintf(" line %d", i))
	}
	js, _ := json.Marshal(map[string]any{"number": 1, "state": "OPEN", "headRefOid": "a",
		"reviewThreads": map[string]any{"nodes": []any{map[string]any{
			"id": "T1", "path": "a.go", "line": 20, "startLine": 18,
			"hunk":     map[string]any{"nodes": []any{map[string]any{"diffHunk": strings.Join(hunk, "\r\n")}}},
			"comments": map[string]any{"totalCount": 0, "nodes": []any{}},
		}}}})
	var p prNode
	if err := json.Unmarshal(js, &p); err != nil {
		t.Fatal(err)
	}
	th := p.toItem("pr:o/r#1", "o/r").Threads[1]
	lines := strings.Split(th.Hunk, "\n")
	if th.StartLine != 18 || th.Line != 20 || len(lines) != hunkLines || lines[len(lines)-1] != " line 20" || strings.Contains(th.Hunk, "\r") {
		t.Errorf("thread = start %d, line %d, hunk %q", th.StartLine, th.Line, th.Hunk)
	}
	if !strings.Contains(prSelection, "hunk: comments(first: 1) { nodes { diffHunk } }") {
		t.Error("the query does not ask for the thread's hunk")
	}
}
