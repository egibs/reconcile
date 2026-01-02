package identity

import (
	"hash/maphash"
	"sync"
	"unsafe"
)

// High bit to distinguish exact matches from identity matches within a shared map.
const ExactFlag uint64 = 1 << 63

// HashAll computes the identity and exact hashes for all strings in parallel.
func HashAll(files []string, workers int, seed maphash.Seed) ([]uint64, []uint64) {
	length := len(files)
	if length == 0 {
		return []uint64{}, []uint64{}
	}

	idMatch, exMatch := make([]uint64, length), make([]uint64, length)

	chunk := max(1, (length+workers-1)/workers)

	var wg sync.WaitGroup

	for w := range workers {
		low := w * chunk
		if low >= length {
			break
		}

		high := min(low+chunk, length)

		wg.Go(func() {
			for i := low; i < high; i++ {
				idMatch[i], exMatch[i] = Hash(files[i], seed)
			}
		})
	}
	wg.Wait()

	return idMatch, exMatch
}

// Hash computes the identity hash and exact match hash for a file path.
// Both hashes have the high bit cleared to leave room for the exactMatch flag.
func Hash(s string, seed maphash.Seed) (uint64, uint64) {
	bs := unsafe.Slice(unsafe.StringData(s), len(s))
	length := len(bs)

	exact := maphash.Bytes(seed, bs) &^ ExactFlag

	if length == 0 {
		return exact, exact
	}

	if i := Soname(bs); i > 0 {
		return maphash.Bytes(seed, bs[:i]) &^ ExactFlag, exact
	}

	if i, j := Script(bs); i > 0 {
		return (maphash.Bytes(seed, bs[:i]) ^ maphash.Bytes(seed, bs[j:])) &^ ExactFlag, exact
	}

	if i, j := Embedded(bs); i > 0 {
		return (maphash.Bytes(seed, bs[:i]) ^ maphash.Bytes(seed, bs[j:])) &^ ExactFlag, exact
	}

	if i := Suffix(bs); i > 0 {
		return maphash.Bytes(seed, bs[:i]) &^ ExactFlag, exact
	}

	return exact, exact
}
