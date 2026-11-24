// Package kind defines the item-kind extension point: a kind parses user
// input into item IDs, fetches the current state of its items in one
// batched request, and knows when an item is finished.
package kind

import (
	"context"
	"fmt"
	"strings"

	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/model"
)

// Kind is one type of watched item (v1: pull requests).
type Kind interface {
	// Name is the ID namespace, e.g. "pr" for "pr:owner/repo#123".
	Name() string
	// Parse turns a URL, short reference or namespaced ID into a
	// canonical item ID. ok is false when the input is not of this kind.
	Parse(s string) (id string, ok bool)
	// Stub returns a placeholder for an item that has not been fetched
	// yet, with whatever can be derived from its ID.
	Stub(id string) model.Item
	// Fetch returns the current state of ids, batched into as few
	// requests as possible. Items that cannot be fetched are returned
	// with Error set. rl is the rate limit after the last request, if known.
	Fetch(ctx context.Context, gh *github.Client, ids []string) (items []model.Item, rl *github.RateLimit, err error)
}

// Retester is implemented by kinds that can re-trigger failed CI.
type Retester interface {
	// Retest re-triggers the failed checks of it and describes what it did.
	Retest(ctx context.Context, gh *github.Client, it model.Item) (string, error)
}

// Discusser is implemented by kinds whose items have comment threads the
// user can take part in.
type Discusser interface {
	// Reply posts body in a thread of it: model.ConversationID for the
	// item's main conversation, else a review thread's ID.
	Reply(ctx context.Context, gh *github.Client, it model.Item, thread, body string) error
	// Resolve marks a review thread resolved, or unresolved.
	Resolve(ctx context.Context, gh *github.Client, it model.Item, thread string, resolved bool) error
}

// Discoverer is implemented by kinds that can find items to watch on
// their own: those for which the user's review is requested
// (model.AddedReview) or that are assigned to the user
// (model.AddedAssigned).
type Discoverer interface {
	// Discover returns the IDs of the open items matching any of reasons,
	// each with the reasons it matches.
	Discover(ctx context.Context, gh *github.Client, reasons []string) (map[string][]string, error)
}

// Registry holds the kinds compiled into ghwatch.
type Registry struct {
	kinds []Kind
}

// NewRegistry returns a registry of the given kinds; the first kind
// whose Parse accepts an input wins.
func NewRegistry(kinds ...Kind) *Registry { return &Registry{kinds: kinds} }

// All returns the registered kinds.
func (r *Registry) All() []Kind { return r.kinds }

// Get returns the kind with the given name.
func (r *Registry) Get(name string) Kind {
	for _, k := range r.kinds {
		if k.Name() == name {
			return k
		}
	}
	return nil
}

// Parse infers the kind of s and returns its canonical ID.
func (r *Registry) Parse(s string) (Kind, string, error) {
	s = strings.TrimSpace(s)
	for _, k := range r.kinds {
		if id, ok := k.Parse(s); ok {
			return k, id, nil
		}
	}
	return nil, "", fmt.Errorf("unrecognized item %q (want a PR URL or owner/repo#number)", s)
}

// Of returns the kind of a canonical ID.
func (r *Registry) Of(id string) Kind {
	name, _, _ := strings.Cut(id, ":")
	return r.Get(name)
}
