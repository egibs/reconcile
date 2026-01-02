package identity

import "sync/atomic"

// TryMark attempts to atomically set bit j in the bitset.
// Returns true the bit is successfully set or false if it is already set.
// Uses compare-and-swap for lock-free updates.
func TryMark(v []atomic.Uint64, j uint32) bool {
	idx, bit := j>>6, uint64(1)<<(j&63)

	for {
		old := v[idx].Load()

		// The bit has already been set by another goroutine.
		if old&bit != 0 {
			return false
		}

		// The bit was successfully set.
		if v[idx].CompareAndSwap(old, old|bit) {
			return true
		}
	}
}

// IsMarked checks if bit j is set in the bitset (via TryMark above).
func IsMarked(v []atomic.Uint64, j uint32) bool {
	return v[j>>6].Load()&(1<<(j&63)) != 0
}
