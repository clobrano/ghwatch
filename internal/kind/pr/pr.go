// Package pr is the pull-request item kind.
package pr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/model"
)

// Name is the ID namespace of pull requests.
const Name = "pr"

// batchSize caps the pull requests per GraphQL request, to stay well
// under GitHub's node limit with 100 check contexts each.
const batchSize = 40

// Kind implements kind.Kind for pull requests.
type Kind struct{}

// New returns the pull-request kind.
func New() Kind { return Kind{} }

// Name implements kind.Kind.
func (Kind) Name() string { return Name }

var (
	namePart = `[A-Za-z0-9_.-]+`
	urlRE    = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/(` + namePart + `)/(` + namePart + `)/pull/(\d+)(?:[/?#].*)?$`)
	refRE    = regexp.MustCompile(`^(?:pr:)?(` + namePart + `)/(` + namePart + `)#(\d+)$`)
)

// Parse implements kind.Kind. It accepts a PR URL, owner/repo#N or
// pr:owner/repo#N.
func (Kind) Parse(s string) (string, bool) {
	s = strings.TrimSpace(s)
	m := urlRE.FindStringSubmatch(s)
	if m == nil {
		m = refRE.FindStringSubmatch(s)
	}
	if m == nil {
		return "", false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return "", false
	}
	return ID(m[1]+"/"+m[2], n), true
}

// ID returns the canonical ID of a pull request.
func ID(repo string, number int) string {
	return fmt.Sprintf("%s:%s#%d", Name, strings.ToLower(repo), number)
}

// Stub implements kind.Kind.
func (Kind) Stub(id string) model.Item {
	it := model.Item{Kind: Name, ID: id, Lifecycle: model.Open, State: model.Pending, Checks: []model.Check{}}
	if owner, repo, n, err := Split(id); err == nil {
		it.Repo, it.Number = owner+"/"+repo, n
		it.URL = fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, n)
	}
	return it
}

// Split returns the owner, repository name and number of an ID.
func Split(id string) (owner, repo string, number int, err error) {
	m := refRE.FindStringSubmatch(id)
	if m == nil {
		return "", "", 0, fmt.Errorf("not a pull request ID: %q", id)
	}
	n, _ := strconv.Atoi(m[3])
	return m[1], m[2], n, nil
}

// Fetch implements kind.Kind.
func (k Kind) Fetch(ctx context.Context, gh *github.Client, ids []string) ([]model.Item, *github.RateLimit, error) {
	var (
		items []model.Item
		rl    *github.RateLimit
	)
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		got, r, err := k.fetchBatch(ctx, gh, ids[start:end])
		if err != nil {
			return items, rl, err
		}
		items = append(items, got...)
		rl = r
	}
	return items, rl, nil
}

func (Kind) fetchBatch(ctx context.Context, gh *github.Client, ids []string) ([]model.Item, *github.RateLimit, error) {
	query, err := buildQuery(ids)
	if err != nil {
		return nil, nil, err
	}
	resp, err := gh.GraphQL(ctx, query, nil)
	if err != nil {
		return nil, nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}
	errsByAlias := map[string]string{}
	for _, e := range resp.Errors {
		if len(e.Path) > 0 {
			if a, ok := e.Path[0].(string); ok {
				errsByAlias[a] = e.Message
			}
		}
	}
	var rl *github.RateLimit
	if raw, ok := data["rateLimit"]; ok {
		rl = &github.RateLimit{}
		if err := json.Unmarshal(raw, rl); err != nil {
			rl = nil
		}
	}
	now := time.Now().UTC()
	items := make([]model.Item, 0, len(ids))
	for i, id := range ids {
		alias := fmt.Sprintf("p%d", i)
		owner, repo, number, _ := Split(id)
		it := model.Item{
			Kind: Name, ID: id, Repo: owner + "/" + repo, Number: number,
			URL: fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number),
		}
		var r repoNode
		if raw := data[alias]; raw != nil {
			_ = json.Unmarshal(raw, &r)
		}
		if r.PullRequest == nil {
			it.Error = errsByAlias[alias]
			if it.Error == "" {
				it.Error = "pull request not found"
			}
			items = append(items, it)
			continue
		}
		it = r.PullRequest.toItem(id, r.NameWithOwner)
		it.UpdatedAt = now
		items = append(items, it)
	}
	return items, rl, nil
}

// buildQuery writes one aliased repository/pullRequest selection per ID.
// Each selection is inlined rather than a fragment, because isRequired
// takes the pull request number as an argument.
func buildQuery(ids []string) (string, error) {
	var b strings.Builder
	b.WriteString("query {\n")
	for i, id := range ids {
		owner, repo, n, err := Split(id)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  p%d: repository(owner: %s, name: %s) {\n    nameWithOwner\n    pullRequest(number: %d) {%s}\n  }\n",
			i, strconv.Quote(owner), strconv.Quote(repo), n, strings.ReplaceAll(prSelection, "$N", strconv.Itoa(n)))
	}
	b.WriteString("  rateLimit { limit remaining resetAt cost }\n}\n")
	return b.String(), nil
}

const prSelection = `
      number title url state merged headRefName headRefOid baseRefName
      author { login }
      labels(first: 20) { nodes { name color } }
      mergeQueueEntry { state position enqueuedAt estimatedTimeToMerge mergeQueue { url } }
      comments(last: 20) { totalCount nodes { id author { login } bodyText createdAt url viewerDidAuthor } }
      reviews(last: 20) { nodes { id author { login } bodyText state submittedAt url viewerDidAuthor } }
      viewerLatestReview { state submittedAt }
      viewerLatestReviewRequest { requestedReviewer { __typename ... on Team { name } } }
      reviewThreads(last: 50) { nodes {
        id isResolved isOutdated path line originalLine
        comments(last: 5) { totalCount nodes { id author { login } bodyText createdAt url viewerDidAuthor } }
      } }
      commits(last: 1) { nodes { commit {
        oid committedDate
        statusCheckRollup { contexts(first: 100) { nodes {
          __typename
          ... on CheckRun {
            name status conclusion startedAt completedAt detailsUrl databaseId
            isRequired(pullRequestNumber: $N)
            checkSuite { app { slug name } }
          }
          ... on StatusContext {
            context state targetUrl createdAt
            isRequired(pullRequestNumber: $N)
          }
        } } }
      } } }
    `

type repoNode struct {
	NameWithOwner string  `json:"nameWithOwner"`
	PullRequest   *prNode `json:"pullRequest"`
}

type prNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	State       string `json:"state"`
	Merged      bool   `json:"merged"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	Author      *struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels struct {
		Nodes []model.Label `json:"nodes"`
	} `json:"labels"`
	MergeQueueEntry *struct {
		State                string    `json:"state"`
		Position             int       `json:"position"`
		EnqueuedAt           time.Time `json:"enqueuedAt"`
		EstimatedTimeToMerge *int      `json:"estimatedTimeToMerge"` // seconds
		MergeQueue           *struct {
			URL string `json:"url"`
		} `json:"mergeQueue"`
	} `json:"mergeQueueEntry"`
	Comments struct {
		TotalCount int           `json:"totalCount"`
		Nodes      []commentNode `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []commentNode `json:"nodes"`
	} `json:"reviews"`
	ViewerLatestReview *struct {
		State       string     `json:"state"`
		SubmittedAt *time.Time `json:"submittedAt"`
	} `json:"viewerLatestReview"`
	ViewerLatestReviewRequest *struct {
		RequestedReviewer *struct {
			Typename string `json:"__typename"`
			Name     string `json:"name"` // teams
		} `json:"requestedReviewer"`
	} `json:"viewerLatestReviewRequest"`
	ReviewThreads struct {
		Nodes []struct {
			ID           string `json:"id"`
			IsResolved   bool   `json:"isResolved"`
			IsOutdated   bool   `json:"isOutdated"`
			Path         string `json:"path"`
			Line         *int   `json:"line"`
			OriginalLine *int   `json:"originalLine"`
			Comments     struct {
				TotalCount int           `json:"totalCount"`
				Nodes      []commentNode `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Oid               string    `json:"oid"`
				CommittedDate     time.Time `json:"committedDate"`
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []json.RawMessage `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (p *prNode) toItem(id, repo string) model.Item {
	it := model.Item{
		Kind: Name, ID: id, Repo: repo, Number: p.Number, Title: p.Title, URL: p.URL,
		Branch: p.HeadRefName, HeadSHA: p.HeadRefOid, Lifecycle: model.Open,
	}
	if p.Author != nil {
		it.Author = p.Author.Login
	}
	it.Labels = p.Labels.Nodes
	switch {
	case p.Merged || p.State == "MERGED":
		it.Lifecycle = model.LifeMerged
	case p.State == "CLOSED":
		it.Lifecycle = model.LifeClosed
	}
	if q := p.MergeQueueEntry; q != nil && it.Lifecycle == model.Open {
		it.MergeQueue = &model.MergeQueue{State: strings.ToLower(q.State), Position: q.Position, EnqueuedAt: q.EnqueuedAt}
		if q.MergeQueue != nil && q.MergeQueue.URL != "" {
			it.MergeQueue.URL = q.MergeQueue.URL
		} else if p.BaseRefName != "" {
			it.MergeQueue.URL = fmt.Sprintf("https://github.com/%s/queue/%s", repo, p.BaseRefName)
		}
		if q.EstimatedTimeToMerge != nil {
			it.MergeQueue.ETASeconds = *q.EstimatedTimeToMerge
		}
	}
	if len(p.Commits.Nodes) > 0 {
		c := p.Commits.Nodes[0].Commit
		it.PushedAt = c.CommittedDate
		if it.HeadSHA == "" {
			it.HeadSHA = c.Oid
		}
		if c.StatusCheckRollup != nil {
			it.Checks = Normalize(c.StatusCheckRollup.Contexts.Nodes, p.URL)
		}
	}
	if it.Checks == nil {
		it.Checks = []model.Check{}
	}
	it.Threads = p.threads()
	if r := p.ViewerLatestReviewRequest; r != nil {
		it.Review.Requested = true
		if rr := r.RequestedReviewer; rr != nil && rr.Typename == "Team" {
			it.Review.Team = rr.Name
		}
	}
	if r := p.ViewerLatestReview; r != nil && r.State != "" {
		it.Review.State = strings.ToLower(r.State)
		if r.SubmittedAt != nil {
			it.Review.At = *r.SubmittedAt
		}
	}
	it.State = model.ItemState(it)
	return it
}

// commentNode is an issue comment, a review or a review comment: the
// fields of GitHub's Comment interface ghwatch uses, plus a review's.
type commentNode struct {
	ID     string `json:"id"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	BodyText        string     `json:"bodyText"`
	CreatedAt       *time.Time `json:"createdAt"`
	SubmittedAt     *time.Time `json:"submittedAt"` // reviews
	State           string     `json:"state"`       // reviews
	URL             string     `json:"url"`
	ViewerDidAuthor bool       `json:"viewerDidAuthor"`
}

func (c commentNode) toComment() model.Comment {
	out := model.Comment{ID: c.ID, Body: text(c.BodyText), URL: c.URL, Mine: c.ViewerDidAuthor}
	if c.Author != nil {
		out.Author = c.Author.Login
	} else {
		out.Author = "ghost" // a deleted account
	}
	switch {
	case c.SubmittedAt != nil:
		out.CreatedAt = *c.SubmittedAt
	case c.CreatedAt != nil:
		out.CreatedAt = *c.CreatedAt
	}
	if c.State != "" {
		out.Review = strings.ToLower(c.State)
		if out.Body == "" {
			out.Body = strings.ReplaceAll(out.Review, "_", " ")
		}
	}
	return out
}

// text is a comment's text without surrounding blank space, cut at
// model.BodyLimit characters.
func text(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if r := []rune(body); len(r) > model.BodyLimit {
		return string(r[:model.BodyLimit-1]) + "…"
	}
	return body
}

// threads returns the pull request's conversation, then its review
// threads in GitHub's order.
func (p *prNode) threads() []model.Thread {
	conv := model.Thread{ID: model.ConversationID, URL: p.URL, Total: p.Comments.TotalCount}
	for _, c := range p.Comments.Nodes {
		conv.Comments = append(conv.Comments, c.toComment())
	}
	for _, r := range p.Reviews.Nodes {
		// A pending review is the viewer's unsent draft; a "commented"
		// review without text only wraps comments on code, which are in
		// their threads.
		if r.State == "PENDING" || r.State == "COMMENTED" && strings.TrimSpace(r.BodyText) == "" {
			continue
		}
		conv.Comments = append(conv.Comments, r.toComment())
		conv.Total++
	}
	sort.SliceStable(conv.Comments, func(i, j int) bool { return conv.Comments[i].CreatedAt.Before(conv.Comments[j].CreatedAt) })
	if n := len(conv.Comments); n > 20 {
		conv.Comments = conv.Comments[n-20:]
	}
	if conv.Comments == nil {
		conv.Comments = []model.Comment{}
	}
	out := []model.Thread{conv}
	for _, t := range p.ReviewThreads.Nodes {
		th := model.Thread{ID: t.ID, Path: t.Path, Resolved: t.IsResolved, Outdated: t.IsOutdated, Total: t.Comments.TotalCount, URL: p.URL + "/files"}
		switch {
		case t.Line != nil:
			th.Line = *t.Line
		case t.OriginalLine != nil:
			th.Line = *t.OriginalLine
		}
		for _, c := range t.Comments.Nodes {
			th.Comments = append(th.Comments, c.toComment())
		}
		if th.Comments == nil {
			th.Comments = []model.Comment{}
		}
		out = append(out, th)
	}
	return out
}

// Retest implements kind.Retester: it re-runs the failed jobs of every
// GitHub Actions workflow run with a failed check, and comments /retest
// when a failed check is reported by Prow.
func (Kind) Retest(ctx context.Context, gh *github.Client, it model.Item) (string, error) {
	owner, repo, number, err := Split(it.ID)
	if err != nil {
		return "", err
	}
	runs := map[string]bool{}
	prow := false
	for _, c := range it.Checks {
		if c.State != model.Failed {
			continue
		}
		if c.Source == SourceProw {
			prow = true
		}
		link := c.DetailsURL // an app's own page; an Actions job's link is its job page

		if link == "" {
			link = c.URL
		}
		if m := runRE.FindStringSubmatch(link); m != nil {
			runs[m[1]] = true
		}
	}
	if !prow && len(runs) == 0 {
		return "", fmt.Errorf("no failed check that can be re-run")
	}
	var done []string
	ids := make([]string, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		path := fmt.Sprintf("/repos/%s/%s/actions/runs/%s/rerun-failed-jobs", owner, repo, id)
		if err := gh.REST(ctx, http.MethodPost, path, nil, nil); err != nil {
			return strings.Join(done, ", "), fmt.Errorf("re-run workflow run %s: %w", id, err)
		}
		done = append(done, "re-ran workflow run "+id)
	}
	if prow {
		path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
		if err := gh.REST(ctx, http.MethodPost, path, map[string]string{"body": "/retest"}, nil); err != nil {
			return strings.Join(done, ", "), fmt.Errorf("comment /retest: %w", err)
		}
		done = append(done, "commented /retest")
	}
	return strings.Join(done, ", "), nil
}

var runRE = regexp.MustCompile(`/actions/runs/(\d+)`)

// Reply implements kind.Discusser: a new comment on the pull request for
// its conversation, or a reply in a review thread.
func (Kind) Reply(ctx context.Context, gh *github.Client, it model.Item, thread, body string) error {
	if thread == model.ConversationID {
		owner, repo, number, err := Split(it.ID)
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
		return gh.REST(ctx, http.MethodPost, path, map[string]string{"body": body}, nil)
	}
	return mutate(ctx, gh, `mutation($thread: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $thread, body: $body}) { comment { id } }
}`, map[string]any{"thread": thread, "body": body})
}

// Resolve implements kind.Discusser.
func (Kind) Resolve(ctx context.Context, gh *github.Client, it model.Item, thread string, resolved bool) error {
	if thread == model.ConversationID {
		return fmt.Errorf("the conversation cannot be resolved, only threads on code")
	}
	op := "resolveReviewThread"
	if !resolved {
		op = "unresolveReviewThread"
	}
	return mutate(ctx, gh, `mutation($thread: ID!) { `+op+`(input: {threadId: $thread}) { thread { id isResolved } } }`,
		map[string]any{"thread": thread})
}

// mutate runs a GraphQL mutation; any error in the response fails it.
func mutate(ctx context.Context, gh *github.Client, query string, vars map[string]any) error {
	resp, err := gh.GraphQL(ctx, query, vars)
	if err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return &github.Error{Message: resp.Errors[0].Message}
	}
	return nil
}

// searchQueries are the GitHub searches behind each reason: review
// requested from the user (not a team), and assigned to the user.
var searchQueries = map[string]string{
	model.AddedReview:   "is:pr is:open archived:false user-review-requested:@me",
	model.AddedAssigned: "is:pr is:open archived:false assignee:@me",
}

// discoverLimit is how many PRs one search returns; the daemon caps how
// many it watches.
const discoverLimit = 50

// Discover implements kind.Discoverer with one GraphQL request holding a
// search per reason.
func (Kind) Discover(ctx context.Context, gh *github.Client, reasons []string) (map[string][]string, error) {
	var b strings.Builder
	b.WriteString("query {\n")
	for i, r := range reasons {
		q, ok := searchQueries[r]
		if !ok {
			return nil, fmt.Errorf("unknown reason %q", r)
		}
		fmt.Fprintf(&b, "  s%d: search(query: %s, type: ISSUE, first: %d) { nodes { ... on PullRequest { number repository { nameWithOwner } } } }\n",
			i, strconv.Quote(q), discoverLimit)
	}
	b.WriteString("}\n")
	resp, err := gh.GraphQL(ctx, b.String(), nil)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, &github.Error{Message: resp.Errors[0].Message}
	}
	var data map[string]struct {
		Nodes []struct {
			Number     int `json:"number"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	out := map[string][]string{}
	for i, r := range reasons {
		for _, n := range data[fmt.Sprintf("s%d", i)].Nodes {
			if n.Number == 0 || n.Repository.NameWithOwner == "" {
				continue // not a pull request
			}
			id := ID(n.Repository.NameWithOwner, n.Number)
			out[id] = append(out[id], r)
		}
	}
	return out, nil
}
