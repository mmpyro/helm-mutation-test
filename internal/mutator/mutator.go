// Package mutator generates candidate mutations for a chart source file.
//
// Every mutator reports byte-range edits against the *original* file bytes; it
// never rewrites the file itself. That keeps mutations precise (formatting is
// preserved exactly) and lets the executor apply and revert a single edit cheaply.
package mutator

import (
	"fmt"
	"sort"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// Mutator IDs. These are user-facing (--mutators) and appear in reports, so they
// are stable identifiers, not display strings.
const (
	IDCondNegate     = "cond-negate"
	IDBoolFlip       = "bool-flip"
	IDNumLiteral     = "num-literal"
	IDStrLiteral     = "str-literal"
	IDYAMLKeyDelete  = "yaml-key-delete"
	IDDefaultDrop    = "default-drop"
	IDComparisonSwap = "comparison-swap"
	IDRequiredDrop   = "required-drop"
)

// Candidate is a single proposed edit to a source file.
type Candidate struct {
	// Mutator is the ID of the mutator that produced this candidate.
	Mutator string
	// Start and End delimit the replaced span in the original bytes.
	Start, End int
	// Replacement is the text substituted for [Start,End).
	Replacement string
	// Note optionally distinguishes variants of the same mutator at one site
	// (e.g. num-literal's "+1" and "zero" variants), so IDs stay unique.
	Note string
}

// Mutator proposes candidates for one kind of mutation.
type Mutator interface {
	// ID is the stable identifier used by --mutators and in reports.
	ID() string
	// Describe is a one-line human explanation, shown in --help.
	Describe() string
	// Mutate returns candidates for the file, in any order. Returning no
	// candidates is normal and not an error.
	Mutate(f *source.File) []Candidate
}

// registry holds every known mutator, keyed by ID.
var registry = map[string]Mutator{}

// register adds a mutator to the registry. It panics on a duplicate ID, which
// can only be a programming error at init time.
func register(m Mutator) {
	if _, dup := registry[m.ID()]; dup {
		panic(fmt.Sprintf("mutator %q registered twice", m.ID()))
	}
	registry[m.ID()] = m
}

// IDs returns every registered mutator ID, sorted for deterministic output.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Get returns the mutator with the given ID.
func Get(id string) (Mutator, bool) {
	m, ok := registry[id]
	return m, ok
}

// Select returns the mutators for the given IDs, in sorted ID order so mutant
// generation is deterministic regardless of flag ordering. Unknown IDs are
// skipped; config validation rejects them before we get here.
func Select(ids []string) []Mutator {
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)

	out := make([]Mutator, 0, len(sorted))
	for _, id := range sorted {
		if m, ok := registry[id]; ok {
			out = append(out, m)
		}
	}
	return out
}

// Descriptions returns "id: description" lines for every mutator, for --help.
func Descriptions() []string {
	out := make([]string, 0, len(registry))
	for _, id := range IDs() {
		out = append(out, fmt.Sprintf("%-16s %s", id, registry[id].Describe()))
	}
	return out
}
