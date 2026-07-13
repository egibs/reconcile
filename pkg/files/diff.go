package files

import (
	"hash/maphash"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/egibs/reconcile/internal/identity"
)

const (
	null      uint32 = 0xFFFFFFFF // Sentinel value for unset file indices
	maxShards        = 1 << 8     // Upper bound on bucket-map partitions
	// smallInput is the approximate number of entries a single worker handles
	// efficiently; inputs below workers*smallInput scale the worker count down
	// so small diffs run sequentially without goroutine and shard overhead.
	smallInput = 2048
)

// This seed is initialized once at package load time for consistent hashing
// and ensures deterministic results across calls.
var seed = maphash.MakeSeed()

// EqualFunc determines whether two strings share the same identity.
// It must be symmetric: EqualFunc(a, b) == EqualFunc(b, a).
type EqualFunc func(old, cur string) bool

// HashFunc computes the identity and exact hashes for a string.
// The first return value is the identity hash (version-stripped); the second is
// the exact hash (full string). Bit 63 (identity.ExactFlag) of both values is
// reserved: DiffWith clears it on identity hashes and sets it on exact hashes
// before use, so implementations may ignore it. The function is only invoked
// on strings present in the input lists.
type HashFunc func(s string, seed maphash.Seed) (uint64, uint64)

// Option configures DiffWith behavior.
type Option func(*config)

type config struct {
	equalFn EqualFunc
	exactFn EqualFunc
	hashFn  HashFunc
}

func defaultConfig() *config {
	return &config{
		equalFn: identity.Equal,
		exactFn: func(old, cur string) bool { return old == cur },
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

// WithExactEquality configures the confirmation used for exact-hash matches,
// which defaults to byte equality. Supply this together with a HashFunc whose
// exact hash is computed over a normalized form (for example, a case-folded
// string) so that normalized-equal inputs classify as Unchanged.
func WithExactEquality(exactFn EqualFunc) Option {
	return func(c *config) {
		c.exactFn = exactFn
	}
}

// bucket collects every file index sharing one key. first always holds the
// minimum index; additional indices spill into rest, so most keys stay on the
// inline fast path. pos is a claim cursor used only by the sequential
// reconciler to skip already-claimed candidates in amortized constant time.
type bucket struct {
	first uint32
	pos   uint32
	rest  []uint32
}

// indices returns the bucket's file indices in ascending order, reusing buf.
// rest is sorted here rather than at insert time; first is already minimal.
func (b bucket) indices(buf []uint32) []uint32 {
	buf = append(buf[:0], b.first)
	if b.rest != nil {
		buf = append(buf, b.rest...)
		slices.Sort(buf[1:])
	}
	return buf
}

// candidate returns the bucket's p-th index in insertion order.
// Only meaningful for sequential builds, where insertion order is ascending.
func (b bucket) candidate(p uint32) uint32 {
	if p == 0 {
		return b.first
	}
	return b.rest[p-1]
}

// size returns the number of indices in the bucket.
func (b bucket) size() uint32 {
	return uint32(1 + len(b.rest)) // #nosec G115 -- bounded by input length guard
}

// shard is a single lock-guarded partition of a key → bucket table.
type shard struct {
	mu sync.Mutex
	m  map[uint64]bucket
}

// insert adds idx to the bucket for key, keeping the minimum index in first.
func (s *shard) insert(key uint64, idx uint32) {
	b, ok := s.m[key]
	switch {
	case !ok:
		s.m[key] = bucket{first: idx}
		return
	case idx < b.first:
		b.rest = append(b.rest, b.first)
		b.first = idx
	default:
		b.rest = append(b.rest, idx)
	}
	s.m[key] = b
}

// Diff compares two file lists and returns a Result containing all
// reconciliation entries. Each list must contain fewer than 2^32-1 entries;
// larger inputs panic. Diff is safe to call concurrently.
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
	if cfg.exactFn == nil {
		panic("files: exact EqualFunc must not be nil")
	}
	return diffPWith(old, cur, max(1, runtime.GOMAXPROCS(0)), cfg)
}

// diffP compares two file lists with an explicit worker count.
func diffP(old, cur []string, workers int) *Result {
	return diffPWith(old, cur, workers, defaultConfig())
}

// diffPWith compares two file lists with an explicit worker count and config.
//
// Reconciliation runs in two passes over multi-candidate buckets. Pass 1
// claims every exact match before pass 2 attempts any identity match, so a
// byte-identical pair can never be misreported as Updated or Removed by an
// earlier identity claim. Buckets hold all indices sharing a key, so
// duplicate names and same-identity groups (for example, a soname symlink
// plus its target across a version bump) all remain matchable. Within a key,
// ascending old indices pair with ascending unclaimed cur indices; every
// key's outcome is self-contained, so results are deterministic for any
// worker count or goroutine schedule, and identical across worker counts.
func diffPWith(old, cur []string, workers int, cfg *config) *Result {
	oldFiles, newFiles := len(old), len(cur)
	if oldFiles|newFiles == 0 {
		return &Result{}
	}

	// Entry indices are uint32 with null (0xFFFFFFFF) reserved as a sentinel;
	// reject inputs whose indices would wrap or collide with the sentinel.
	if uint64(oldFiles) >= uint64(null) || uint64(newFiles) >= uint64(null) {
		panic("files: input lists must contain fewer than 4294967295 entries")
	}

	// Scale workers to the input so small diffs run sequentially.
	if limit := (max(oldFiles, newFiles) + smallInput - 1) / smallInput; workers > limit {
		workers = limit
	}
	workers = max(1, workers)

	// The parallel reconciler needs an old-side bucket table; below four
	// workers that extra build costs more than the parallelism returns, so
	// reconcile sequentially (hashing and the additions scan still fan out).
	// Both reconcilers implement identical per-key pairing, so results do
	// not depend on which one runs. The sequential reconciler consumes
	// candidates in insertion order, so its buckets are built single-worker
	// (ascending index order).
	sequentialReconcile := workers < 4
	buildWorkers := workers
	if sequentialReconcile {
		buildWorkers = 1
	}

	// Shard the bucket maps by key so parallel builders rarely contend.
	numShards := 1
	for numShards < workers*8 && numShards < maxShards {
		numShards <<= 1
	}
	shardMask := uint64(numShards - 1) // #nosec G115 -- numShards is a small power of two

	// Calculate hashes for both the old and new files.
	oldID, oldEx := identity.HashAll(old, workers, seed, cfg.hashFn)
	curID, curEx := identity.HashAll(cur, workers, seed, cfg.hashFn)

	// Group cur indices into buckets keyed by identity hash and exact hash.
	// Exact keys use hash | ExactFlag and identity keys use hash &^ ExactFlag,
	// so both live in one table and a custom HashFunc can never leak identity
	// keys into the exact namespace (or vice versa).
	buildBuckets := func(ids, exs []uint64) []shard {
		shards := make([]shard, numShards)
		expected := max(4, 2*len(ids)/numShards)
		for i := range shards {
			shards[i].m = make(map[uint64]bucket, expected)
		}
		identity.ParallelChunks(len(ids), buildWorkers, func(_, low, high int) {
			for i := low; i < high; i++ {
				fileIdx := uint32(i) // #nosec G115 -- input length guarded above
				idKey := ids[i] &^ identity.ExactFlag
				exKey := exs[i] | identity.ExactFlag
				if buildWorkers > 1 {
					s := &shards[idKey&shardMask]
					s.mu.Lock()
					s.insert(idKey, fileIdx)
					s.mu.Unlock()
					s = &shards[exKey&shardMask]
					s.mu.Lock()
					s.insert(exKey, fileIdx)
					s.mu.Unlock()
				} else {
					shards[idKey&shardMask].insert(idKey, fileIdx)
					shards[exKey&shardMask].insert(exKey, fileIdx)
				}
			}
		})
		return shards
	}

	curShards := buildBuckets(curID, curEx)

	// One reconciliation slot per old file, defaulting to Removed. Slots are
	// written by at most one goroutine per pass, since an old index belongs
	// to exactly one exact bucket and one identity bucket.
	oldEntries := make([]Entry, oldFiles)
	identity.ParallelChunks(oldFiles, workers, func(_, low, high int) {
		for i := low; i < high; i++ {
			oldEntries[i] = Entry{uint32(i), null, Removed} // #nosec G115 -- guarded above
		}
	})

	// Bitset of claimed cur files. Distinct bits sharing a word may be set
	// concurrently (atomic OR); a given bit is only ever set by the one
	// goroutine that owns its bucket in the current pass.
	matches := make([]atomic.Uint64, (newFiles+63)>>6)

	var unchanged, updated uint32
	if sequentialReconcile {
		unchanged, updated = reconcileSequential(old, cur, oldEx, oldID, curShards, shardMask, matches, oldEntries, cfg)
	} else {
		oldShards := buildBuckets(oldID, oldEx)
		unchanged, updated = reconcileParallel(old, cur, oldEx, oldID, oldShards, curShards, shardMask, matches, oldEntries, cfg, workers)
	}

	// Unclaimed cur files are additions. Chunk ordinals keep additions in
	// ascending cur-index order for deterministic output.
	additions := make([][]Entry, workers)
	identity.ParallelChunks(newFiles, workers, func(w, low, high int) {
		entries := make([]Entry, 0, (high-low)/4)
		for i := low; i < high; i++ {
			fileIdx := uint32(i) // #nosec G115 -- guarded above
			if !identity.IsMarked(matches, fileIdx) {
				entries = append(entries, Entry{null, fileIdx, Added})
			}
		}
		additions[w] = entries
	})

	added := 0
	for _, a := range additions {
		added += len(a)
	}

	result := &Result{E: make([]Entry, 0, oldFiles+added)}
	result.E = append(result.E, oldEntries...)
	for _, entries := range additions {
		result.E = append(result.E, entries...)
	}

	result.C = [4]uint32{unchanged, updated, uint32(oldFiles) - unchanged - updated, uint32(added)} // #nosec G115 -- guarded above

	return result
}

// reconcileSequential runs both reconciliation passes on one goroutine.
// Buckets were built in ascending index order, so candidate order needs no
// sorting, and the per-bucket claim cursor keeps duplicate-heavy inputs
// linear. Ascending old indices claim ascending unclaimed cur candidates —
// exactly the per-key pairing the parallel reconciler produces.
func reconcileSequential(old, cur []string, oldEx, oldID []uint64, curShards []shard, shardMask uint64, matches []atomic.Uint64, oldEntries []Entry, cfg *config) (unchanged, updated uint32) {
	// Pass 1: exact matches.
	for i := range old {
		key := oldEx[i] | identity.ExactFlag
		s := &curShards[key&shardMask]
		b, ok := s.m[key]
		if !ok {
			continue
		}
		size := b.size()
		p := b.pos
		for p < size && identity.IsMarked(matches, b.candidate(p)) {
			p++
		}
		for q := p; q < size; q++ {
			cj := b.candidate(q)
			if identity.IsMarked(matches, cj) {
				continue
			}
			if cfg.exactFn(old[i], cur[cj]) {
				identity.Mark(matches, cj)
				oldEntries[i] = Entry{uint32(i), cj, Unchanged} // #nosec G115 -- guarded by caller
				unchanged++
				break
			}
		}
		if p != b.pos {
			b.pos = p
			s.m[key] = b
		}
	}

	// Pass 2: identity matches for old files without an exact match.
	for i := range old {
		if oldEntries[i].New != null {
			continue
		}
		key := oldID[i] &^ identity.ExactFlag
		s := &curShards[key&shardMask]
		b, ok := s.m[key]
		if !ok {
			continue
		}
		size := b.size()
		p := b.pos
		for p < size && identity.IsMarked(matches, b.candidate(p)) {
			p++
		}
		for q := p; q < size; q++ {
			cj := b.candidate(q)
			if identity.IsMarked(matches, cj) {
				continue
			}
			if cfg.equalFn(old[i], cur[cj]) {
				identity.Mark(matches, cj)
				oldEntries[i] = Entry{uint32(i), cj, Updated} // #nosec G115 -- guarded by caller
				updated++
				break
			}
		}
		if p != b.pos {
			b.pos = p
			s.m[key] = b
		}
	}

	return unchanged, updated
}

// reconcileParallel runs both reconciliation passes across workers. Old files
// are grouped by key; the group's minimum old index acts as leader and pairs
// the entire group, so every key is processed by exactly one goroutine and
// results match the sequential reconciler. The pass boundary is a barrier:
// all exact claims are visible before any identity claim is attempted.
func reconcileParallel(old, cur []string, oldEx, oldID []uint64, oldShards, curShards []shard, shardMask uint64, matches []atomic.Uint64, oldEntries []Entry, cfg *config, workers int) (unchanged, updated uint32) {
	var unchangedA, updatedA atomic.Uint32

	// Pass 1: exact matches.
	identity.ParallelChunks(len(old), workers, func(_, low, high int) {
		var obuf, cbuf []uint32
		var count uint32
		for i := low; i < high; i++ {
			fileIdx := uint32(i) // #nosec G115 -- guarded by caller
			key := oldEx[i] | identity.ExactFlag
			ob := oldShards[key&shardMask].m[key]
			if ob.first != fileIdx {
				continue // the group leader pairs the whole group
			}
			cb, ok := curShards[key&shardMask].m[key]
			if !ok {
				continue
			}
			if ob.rest == nil && cb.rest == nil {
				// Fast path: single old and cur index for this key.
				if !identity.IsMarked(matches, cb.first) && cfg.exactFn(old[i], cur[cb.first]) {
					identity.Mark(matches, cb.first)
					oldEntries[i] = Entry{fileIdx, cb.first, Unchanged}
					count++
				}
				continue
			}
			obuf, cbuf = ob.indices(obuf), cb.indices(cbuf)
			start := 0
			for _, oi := range obuf {
				for start < len(cbuf) && identity.IsMarked(matches, cbuf[start]) {
					start++
				}
				for p := start; p < len(cbuf); p++ {
					cj := cbuf[p]
					if identity.IsMarked(matches, cj) {
						continue
					}
					if cfg.exactFn(old[oi], cur[cj]) {
						identity.Mark(matches, cj)
						oldEntries[oi] = Entry{oi, cj, Unchanged}
						count++
						break
					}
				}
			}
		}
		unchangedA.Add(count)
	})

	// Pass 2: identity matches for old files without an exact match.
	identity.ParallelChunks(len(old), workers, func(_, low, high int) {
		var obuf, cbuf []uint32
		var count uint32
		for i := low; i < high; i++ {
			fileIdx := uint32(i) // #nosec G115 -- guarded by caller
			key := oldID[i] &^ identity.ExactFlag
			ob := oldShards[key&shardMask].m[key]
			if ob.first != fileIdx {
				continue // the group leader pairs the whole group
			}
			cb, ok := curShards[key&shardMask].m[key]
			if !ok {
				continue
			}
			if ob.rest == nil && cb.rest == nil {
				if oldEntries[i].New != null {
					continue
				}
				if !identity.IsMarked(matches, cb.first) && cfg.equalFn(old[i], cur[cb.first]) {
					identity.Mark(matches, cb.first)
					oldEntries[i] = Entry{fileIdx, cb.first, Updated}
					count++
				}
				continue
			}
			obuf, cbuf = ob.indices(obuf), cb.indices(cbuf)
			start := 0
			for _, oi := range obuf {
				if oldEntries[oi].New != null {
					continue // matched exactly in pass 1
				}
				for start < len(cbuf) && identity.IsMarked(matches, cbuf[start]) {
					start++
				}
				for p := start; p < len(cbuf); p++ {
					cj := cbuf[p]
					if identity.IsMarked(matches, cj) {
						continue
					}
					if cfg.equalFn(old[oi], cur[cj]) {
						identity.Mark(matches, cj)
						oldEntries[oi] = Entry{oi, cj, Updated}
						count++
						break
					}
				}
			}
		}
		updatedA.Add(count)
	})

	return unchangedA.Load(), updatedA.Load()
}
