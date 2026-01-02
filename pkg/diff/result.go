package diff

import (
	"iter"
	"sync/atomic"
)

type Status uint8

const (
	Unchanged Status = iota
	Updated
	Removed
	Added
)

// Entry represents a single file reconciliation result.
// For Unchanged and Updated entries, Old and New will contain file indices.
// For Removed entries, New will be null (using the sentinel value of 0xFFFFFFFF).
// For Added entries, Old will be null (using the sentinel value of 0xFFFFFFFF).
type Entry struct {
	Old    uint32
	New    uint32
	Status uint32
}

// Result contains the final reconciliation output for a collection of old and new files.
type Result struct {
	E []Entry          // All Unchanged, Updated, Removed, and Added entries
	C [4]atomic.Uint32 // Counts of the above statuses indexed by their respecive integer values
}

// Count returns the number of entries with the given status.
func (r *Result) Count(s Status) uint32 { return r.C[s].Load() }

// All returns an iterator over all entries with their status.
func (r *Result) All() iter.Seq2[Status, Entry] {
	return func(yield func(Status, Entry) bool) {
		for _, e := range r.E {
			// #nosec G115
			if !yield(Status(e.Status&0xFF), e) {
				return
			}
		}
	}
}

// Filter returns an iterator over entries with a specific status.
func (r *Result) Filter(s Status) iter.Seq[Entry] {
	return func(yield func(Entry) bool) {
		for _, e := range r.E {
			// #nosec G115
			if Status(e.Status&0xFF) == s {
				if !yield(e) {
					return
				}
			}
		}
	}
}
