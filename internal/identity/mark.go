package identity

import "sync/atomic"

// Mark sets bit j in the bitset with an atomic OR.
// Concurrent callers marking distinct bits that share a word are safe;
// callers must ensure each bit is claimed by at most one goroutine.
func Mark(v []atomic.Uint64, j uint32) {
	v[j>>6].Or(uint64(1) << (j & 63))
}

// IsMarked checks if bit j is set in the bitset (via Mark above).
func IsMarked(v []atomic.Uint64, j uint32) bool {
	return v[j>>6].Load()&(1<<(j&63)) != 0
}
