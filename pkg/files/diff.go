package files

import (
	"hash/maphash"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/egibs/reconcile/internal/identity"
)

const (
	null      uint32 = 0xFFFFFFFF // Sentinel value for unset file indices
	numShards        = 1 << shardBits
	shardBits        = 8
	shardMask uint64 = numShards - 1 // Mask for extracting a shard's index from a given hash
)

// This seed is initialized once at package load time for consistent hashing
// and ensures deterministic results across calls.
var seed = maphash.MakeSeed()

// shard represents a single partition of the O(1) hash table.
type shard struct {
	sync.Mutex
	m map[uint64]uint32 // map containing matches (exact keys use hash|hash.ExactFlag; identity keys just use hash)
}

// Diff compares two file lists and returns a Result containing all reconciliation entries.
func Diff(old, cur []string) *Result {
	return diffP(old, cur, max(1, runtime.GOMAXPROCS(0)))
}

// diffP compares two file lists with an explicit worker count.
func diffP(old, cur []string, workers int) *Result {
	oldFiles, newFiles := len(old), len(cur)
	if oldFiles|newFiles == 0 {
		return &Result{}
	}

	// Calculate hashes for both the old and new files.
	oldHashes, oldEntries := identity.HashAll(old, workers, seed)
	curHashes, curEntries := identity.HashAll(cur, workers, seed)

	// Build a map of all new files for O(1) lookups.
	// Exact entry keys use a file's hash OR'd with the exact flag (hash | exactFlag).
	// Identity entry keys just use a file's hash.
	// Both entry values are the file's index.
	// Using a high bit flag allows for both entries to exist in the same map.
	shards := make([]shard, numShards)
	expected := max(16, newFiles/numShards*2)
	for i := range shards {
		shards[i].m = make(map[uint64]uint32, expected)
	}

	chunk := max(1, (newFiles+workers-1)/workers)

	var wg sync.WaitGroup

	for worker := range workers {
		low := worker * chunk
		if low >= newFiles {
			break
		}

		high := min(low+chunk, newFiles)

		wg.Go(func() {
			for i := low; i < high; i++ {
				shard := &shards[curHashes[i]&shardMask]
				fileIdx := uint32(i) // #nosec G115
				idKey := curHashes[i]
				exKey := curEntries[i] | identity.ExactFlag

				shard.Lock()
				// Only store the first identity match (handling deduplication).
				if _, ok := shard.m[idKey]; !ok {
					shard.m[idKey] = fileIdx
				}

				// Always store exact matches (last occurrence takes precedence).
				shard.m[exKey] = fileIdx
				shard.Unlock()
			}
		})
	}
	wg.Wait()

	// Reconcile the old and new file lists.
	// Check for exact matches first and identity matches second; fall back to removal
	// if there are no exact or identity matches.
	// Bitwise operations are used to track matches to ensure that a new file only matches one old file.
	matches := make([]atomic.Uint64, (newFiles+63)>>6) // One bit per new file
	results := make([][]Entry, workers)                // Per-worker reconciliation results
	counts := make([][3]uint32, workers)               // Per-worker statuses excluding Additions which are handled separately

	chunk = max(1, (newFiles+workers-1)/workers)

	for worker := range workers {
		low := worker * chunk
		if low >= oldFiles {
			break
		}

		high := min(low+chunk, oldFiles)

		wg.Go(func() {
			entries := make([]Entry, 0, high-low)
			var status [3]uint32

			for i := low; i < high; i++ {
				fileIdx := uint32(i) // #nosec G115
				shard := &shards[oldHashes[i]&shardMask]
				m := shard.m

				// Check for exact matches first.
				if exMatch, ok := m[oldEntries[i]|identity.ExactFlag]; ok {
					if old[i] == cur[exMatch] && identity.TryMark(matches, exMatch) {
						entries = append(entries, Entry{fileIdx, exMatch, uint32(Unchanged)})
						status[Unchanged]++
						continue
					}
				}

				// Check for identity matches second.
				if idMatch, ok := m[oldHashes[i]]; ok {
					if !identity.IsMarked(matches, idMatch) && identity.Equal(old[i], cur[idMatch]) && identity.TryMark(matches, idMatch) {
						entries = append(entries, Entry{fileIdx, idMatch, uint32(Updated)})
						status[Updated]++
						continue
					}
				}

				// Fall back to removal if there are no matches.
				entries = append(entries, Entry{fileIdx, null, uint32(Removed)})
				status[Removed]++
			}

			results[worker] = entries
			counts[worker] = status
		})
	}
	wg.Wait()

	// Check matched file bits for unmatched files and treat them as additions.
	additions := make([][]Entry, workers)

	chunk = max(1, (newFiles+workers-1)/workers)

	for worker := range workers {
		low := worker * chunk
		if low >= newFiles {
			break
		}

		high := min(low+chunk, newFiles)

		wg.Go(func() {
			entries := make([]Entry, 0, (high-low)/4)

			for i := low; i < high; i++ {
				fileIdx := uint32(i) // #nosec G115

				if !identity.IsMarked(matches, fileIdx) {
					entries = append(entries, Entry{null, fileIdx, uint32(Added)})
				}
			}

			additions[worker] = entries
		})
	}
	wg.Wait()

	// Deterministically merge all of the reconciliation results
	// and additions into a final result type.
	var total int
	for _, r := range results {
		total += len(r)
	}
	for _, a := range additions {
		total += len(a)
	}

	result := &Result{E: make([]Entry, 0, total)}

	for worker, entries := range results {
		result.E = append(result.E, entries...)
		// Only iterate over the Unchanged, Updated, and Removed status values
		// since Additions are handled separately.
		// Added's iota value is `3` so we can iterate over [0..2] contiguously.
		for status := range 3 {
			result.C[status].Add(counts[worker][status])
		}
	}

	for _, entries := range additions {
		result.E = append(result.E, entries...)
		result.C[Added].Add(uint32(len(entries))) // #nosec G115
	}

	return result
}
