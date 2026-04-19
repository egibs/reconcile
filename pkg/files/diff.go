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

// EqualFunc determines whether two strings share the same identity.
// It must be symmetric: EqualFunc(a, b) == EqualFunc(b, a).
type EqualFunc func(old, cur string) bool

// HashFunc computes the identity and exact hashes for a string.
// The first return value is the identity hash (version-stripped); the second is
// the exact hash (full string). Both must have the high bit (1<<63) cleared.
type HashFunc func(s string, seed maphash.Seed) (uint64, uint64)

// Option configures DiffWith behavior.
type Option func(*config)

type config struct {
	equalFn EqualFunc
	hashFn  HashFunc
}

func defaultConfig() *config {
	return &config{
		equalFn: identity.Equal,
		hashFn:  identity.Hash,
	}
}

// WithIdentity configures DiffWith to use custom hash and equality functions.
// hashFn computes identity and exact hashes; equalFn confirms identity matches.
func WithIdentity(hashFn HashFunc, equalFn EqualFunc) Option {
	return func(c *config) {
		c.hashFn = hashFn
		c.equalFn = equalFn
	}
}

// WithAPKIdentity configures DiffWith to use APK-specific identity matching.
// This produces identical results to Diff.
func WithAPKIdentity() Option {
	return WithIdentity(identity.Hash, identity.Equal)
}

// shard represents a single partition of the O(1) hash table.
type shard struct {
	sync.Mutex
	m map[uint64]uint32 // map containing matches (exact keys use hash|hash.ExactFlag; identity keys just use hash)
}

// Diff compares two file lists and returns a Result containing all reconciliation entries.
func Diff(old, cur []string) *Result {
	return DiffWith(old, cur)
}

// DiffWith compares two string lists using the provided options.
// Without options, behavior is identical to Diff.
func DiffWith(old, cur []string, opts ...Option) *Result {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}
	if cfg.hashFn == nil {
		panic("files: HashFunc must not be nil")
	}
	if cfg.equalFn == nil {
		panic("files: EqualFunc must not be nil")
	}
	if h, e := cfg.hashFn("", seed); h&identity.ExactFlag != 0 || e&identity.ExactFlag != 0 {
		panic("files: HashFunc must clear bit 63 (ExactFlag) on both returned hashes")
	}
	return diffPWith(old, cur, max(1, runtime.GOMAXPROCS(0)), cfg)
}

// diffP compares two file lists with an explicit worker count.
func diffP(old, cur []string, workers int) *Result {
	return diffPWith(old, cur, workers, defaultConfig())
}

// diffPWith compares two file lists with an explicit worker count and config.
func diffPWith(old, cur []string, workers int, cfg *config) *Result {
	oldFiles, newFiles := len(old), len(cur)
	if oldFiles|newFiles == 0 {
		return &Result{}
	}

	// Calculate hashes for both the old and new files.
	oldHashes, oldEntries := identity.HashAll(old, workers, seed, cfg.hashFn)
	curHashes, curEntries := identity.HashAll(cur, workers, seed, cfg.hashFn)

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
				// Identity: lowest index wins (deterministic across concurrent workers).
				if existing, ok := shard.m[idKey]; !ok || fileIdx < existing {
					shard.m[idKey] = fileIdx
				}
				// Exact: highest index wins (deterministic across concurrent workers).
				if existing, ok := shard.m[exKey]; !ok || fileIdx > existing {
					shard.m[exKey] = fileIdx
				}
				shard.Unlock()
			}
		})
	}
	wg.Wait()

	// Reconcile the old and new file lists sequentially.
	// Sequential processing ensures the lower old-file index always claims a match
	// slot first when multiple old files share a hash, making results deterministic.
	matches := make([]atomic.Uint64, (newFiles+63)>>6)
	reconciled := make([]Entry, 0, oldFiles)
	var reconCounts [3]uint32

	for i := range oldFiles {
		fileIdx := uint32(i) // #nosec G115
		shard := &shards[oldHashes[i]&shardMask]
		m := shard.m

		if exMatch, ok := m[oldEntries[i]|identity.ExactFlag]; ok {
			if old[i] == cur[exMatch] && identity.TryMark(matches, exMatch) {
				reconciled = append(reconciled, Entry{fileIdx, exMatch, Unchanged})
				reconCounts[Unchanged]++
				continue
			}
		}

		if idMatch, ok := m[oldHashes[i]]; ok {
			if !identity.IsMarked(matches, idMatch) && cfg.equalFn(old[i], cur[idMatch]) && identity.TryMark(matches, idMatch) {
				reconciled = append(reconciled, Entry{fileIdx, idMatch, Updated})
				reconCounts[Updated]++
				continue
			}
		}

		reconciled = append(reconciled, Entry{fileIdx, null, Removed})
		reconCounts[Removed]++
	}

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
					entries = append(entries, Entry{null, fileIdx, Added})
				}
			}

			additions[worker] = entries
		})
	}
	wg.Wait()

	total := len(reconciled)
	for _, a := range additions {
		total += len(a)
	}

	result := &Result{E: make([]Entry, 0, total)}
	result.E = append(result.E, reconciled...)
	for status := range 3 {
		result.C[status].Add(reconCounts[status])
	}

	for _, entries := range additions {
		result.E = append(result.E, entries...)
		result.C[Added].Add(uint32(len(entries))) // #nosec G115
	}

	return result
}
