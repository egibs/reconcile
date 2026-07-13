package identity

import "sync"

// ParallelChunks partitions [0, n) into at most `workers` contiguous ranges
// and invokes fn concurrently on each. w is the zero-based chunk ordinal,
// assigned in ascending range order, so callers can store per-chunk results
// in a slice sized `workers` without synchronization. Blocks until all
// chunks complete.
func ParallelChunks(n, workers int, fn func(w, low, high int)) {
	if n <= 0 {
		return
	}

	chunk := max(1, (n+workers-1)/workers)

	var wg sync.WaitGroup

	for w, low := 0, 0; low < n; w, low = w+1, low+chunk {
		high := min(low+chunk, n)

		wg.Go(func() {
			fn(w, low, high)
		})
	}
	wg.Wait()
}
