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

// Registry holds the kinds compiled into ghwatch.
type Registry struct {
	kinds []Kind
}

// NewRegistry returns a registry of the given kinds; the first kind
// whose Parse accepts an input wins.
func NewRegistry(kinds ...Kind) *Registry { return &Registry{kinds: kinds} }

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
