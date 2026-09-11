// Package processpath models the fleet's process-path catalogue as a
// small, read-only lookup: which process-path families are currently
// active, matched by prefix. This package mirrors wes-work-planning's
// and fulfillment-execution's own pathcatalog package byte-for-byte in
// its matching semantics -- all three read the same published-language
// events from process-path-management, so they must agree. See
// wes-work-planning's ADR-0017 (and its addendum) for the full history
// of why an earlier exact-match version of this idea was wrong, and
// order-management's own ADR-0013 for why this context needs the
// catalogue at all: an OrderLine's resolved PathId is validated against
// it before the order is ever persisted, rather than being discovered
// wrong one saga step later in wes-work-planning.
//
// order-management deliberately keeps a SMALLER shape than WES/FE's
// copy: it only ever asks "is this path active", never
// RequiredCapabilities or Direct, so PathDefinition here carries only
// what IsActive needs. If a future phase adds attribute-driven routing
// (see the deferred Phase 2 in the process-path-selection plan), extend
// this struct then -- don't carry unused fields today.
package processpath

import (
	"errors"
	"strings"
)

// ErrUnknownPath is returned by Lookup when id does not match any
// currently active path's prefix.
var ErrUnknownPath = errors.New("processpath: unknown or inactive process path id")

// PathDefinition is one currently active process path: its canonical id
// and the lower-cased prefix family of real path_id values it
// recognizes.
type PathDefinition struct {
	Id          string
	MatchPrefix string
}

// Catalogue is the validated, in-memory set of currently active process
// paths.
type Catalogue struct {
	defs []PathDefinition
}

// New builds a Catalogue from defs.
func New(defs []PathDefinition) *Catalogue {
	out := make([]PathDefinition, len(defs))
	copy(out, defs)
	return &Catalogue{defs: out}
}

// Lookup returns the declared definition whose MatchPrefix matches id
// (case-insensitively: id equals the prefix, or id starts with
// prefix + "-"), or ErrUnknownPath if no active path recognizes id. When
// more than one declared prefix would match, the LONGEST matching prefix
// wins.
func (c *Catalogue) Lookup(id string) (PathDefinition, error) {
	lower := strings.ToLower(id)

	var best PathDefinition
	bestLen := -1
	for _, d := range c.defs {
		prefix := strings.ToLower(d.MatchPrefix)
		if prefix == "" {
			continue
		}
		if !matchesPrefix(lower, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			best = d
			bestLen = len(prefix)
		}
	}
	if bestLen == -1 {
		return PathDefinition{}, ErrUnknownPath
	}
	return best, nil
}

// matchesPrefix reports whether id (already lower-cased) belongs to
// prefix's family: either id equals prefix exactly, or id starts with
// prefix followed by a "-" separator. Deliberately does NOT match a bare
// substring prefix without the separator.
func matchesPrefix(id, prefix string) bool {
	if id == prefix {
		return true
	}
	return strings.HasPrefix(id, prefix+"-")
}
