package identity

import (
	"hash/maphash"
	"math/bits"
	"unsafe"
)

// High bit to distinguish exact matches from identity matches within a shared map.
const ExactFlag uint64 = 1 << 63

// HashAll computes the identity and exact hashes for all strings in parallel.
// fn is called for each string to produce (identityHash, exactHash).
func HashAll(files []string, workers int, seed maphash.Seed, fn func(string, maphash.Seed) (uint64, uint64)) ([]uint64, []uint64) {
	length := len(files)
	if length == 0 {
		return []uint64{}, []uint64{}
	}

	idMatch, exMatch := make([]uint64, length), make([]uint64, length)

	ParallelChunks(length, workers, func(_, low, high int) {
		for i := low; i < high; i++ {
			idMatch[i], exMatch[i] = fn(files[i], seed)
		}
	})

	return idMatch, exMatch
}

// Hash computes the identity hash and exact match hash for a file path.
// Both hashes have the high bit cleared to leave room for the ExactFlag.
// The identity spans are determined by Spans, so Hash and Equal always agree
// on what constitutes a file's identity.
func Hash(s string, seed maphash.Seed) (uint64, uint64) {
	bs := unsafe.Slice(unsafe.StringData(s), len(s))
	length := len(bs)

	exact := maphash.Bytes(seed, bs) &^ ExactFlag

	if length == 0 {
		return exact, exact
	}

	j, s2, e := Spans(bs)
	if j == length && s2 == e {
		// No version pattern: the identity is the whole string.
		return exact, exact
	}

	h := maphash.Bytes(seed, bs[:j])
	if s2 < e {
		// Rotating the prefix hash before combining keeps the operation
		// non-commutative and prevents the structural collapse of a plain
		// XOR, where equal prefix and suffix spans always cancel to zero.
		h = bits.RotateLeft64(h, 17) ^ maphash.Bytes(seed, bs[s2:e])
	}

	return h &^ ExactFlag, exact
}
