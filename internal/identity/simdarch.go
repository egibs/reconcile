//go:build goexperiment.simd && (amd64 || arm64)

package identity

import (
	"math/bits"
	"simd/archsimd"
)

// This file contains archsimd (hardware SIMD) variants of the identity
// byte-scan kernels, used to evaluate moving the package's bitwise scans to
// the Go 1.27 simd/archsimd package. All kernels operate on 128-bit vectors,
// which both amd64 (SSE/AVX) and arm64 (Neon) support; the per-architecture
// mask-extraction helpers live in simdarch_amd64.go and simdarch_arm64.go.
//
// Every kernel is behaviorally identical to its scalar counterpart in
// identity.go; simdarch_test.go cross-checks them on fixed corpora and
// randomized inputs.

// digitOrDot returns a mask of lanes that are ASCII digits or '.'.
func digitOrDot(v archsimd.Uint8x16) archsimd.Mask8x16 {
	digit := v.Sub(archsimd.BroadcastUint8x16('0')).Less(archsimd.BroadcastUint8x16(10))
	return digit.Or(v.Equal(archsimd.BroadcastUint8x16('.')))
}

// tailScanSIMD returns the largest index t <= end such that bs[t] is neither
// an ASCII digit nor a dot, or -1 when bs[0:end+1] is all digits and dots.
// It is the vector form of the backward scans in Soname and Embedded.
func tailScanSIMD(bs []byte, end int) int {
	i := end + 1
	for i >= 16 {
		n := runFromTop(digitOrDot(archsimd.LoadUint8x16(bs[i-16 : i])))
		i -= n
		if n < 16 {
			return i - 1
		}
	}
	j := i - 1
	for j >= 0 && (bs[j]-'0' < 10 || bs[j] == '.') {
		j--
	}
	return j
}

// versionScanSIMD scans backward from bs[end] over digits and dots, returning
// the index of the first non-version byte (or -1) and the number of dots in
// the scanned run. It is the vector form of the version scan in Embedded.
func versionScanSIMD(bs []byte, end int) (int, int) {
	i, dots := end+1, 0
	for i >= 16 {
		v := archsimd.LoadUint8x16(bs[i-16 : i])
		n := runFromTop(digitOrDot(v))
		dots += countTop(v.Equal(archsimd.BroadcastUint8x16('.')), n)
		i -= n
		if n < 16 {
			return i - 1, dots
		}
	}
	j := i - 1
	for j >= 0 && (bs[j]-'0' < 10 || bs[j] == '.') {
		if bs[j] == '.' {
			dots++
		}
		j--
	}
	return j, dots
}

// lastDotSIMD returns the index of the last '.' in bs, or -1 if none.
func lastDotSIMD(bs []byte) int {
	i := len(bs)
	dot := archsimd.BroadcastUint8x16('.')
	for i >= 16 {
		if p := lastMatch(archsimd.LoadUint8x16(bs[i-16 : i]).Equal(dot)); p >= 0 {
			return i - 16 + p
		}
		i -= 16
	}
	for j := i - 1; j >= 0; j-- {
		if bs[j] == '.' {
			return j
		}
	}
	return -1
}

// sonameSIMD is the archsimd variant of Soname.
func sonameSIMD(bs []byte) int {
	i := len(bs) - 1
	if i < 4 || bs[i]-'0' >= 10 {
		return 0
	}

	i = tailScanSIMD(bs, i)

	if i >= 2 && bs[i] == 'o' && bs[i-1] == 's' && bs[i-2] == '.' && bs[i+1] == '.' {
		return i + 1
	}

	return 0
}

// embeddedSIMD is the archsimd variant of Embedded.
func embeddedSIMD(bs []byte) (int, int) {
	length := len(bs)
	if length < 9 {
		return 0, 0
	}

	ext := lastDotSIMD(bs)
	if ext < 6 || ext == length-1 {
		return 0, 0
	}

	i, dots := versionScanSIMD(bs, ext-1)

	if dots >= 3 && i >= 0 && bs[i+1] == '.' && bs[i+2]-'0' < 10 {
		return i + 1, ext
	}

	return 0, 0
}

// suffixSIMD is the archsimd variant of Suffix. The extension split uses the
// vector last-dot scan; the version-segment walk stays scalar because its
// per-segment state machine (segment boundaries, per-segment digit rules)
// does not map onto lane-parallel operations.
func suffixSIMD(bs []byte) (int, int) {
	length := len(bs)

	end := length
	if dot := lastDotSIMD(bs); dot > 0 && dot+1 < length && isAlpha(bs[dot+1]) {
		end = dot
	}

	verStart := 0
	segHasDigit := false
	segLen := 0

	for i := end - 1; i >= 0; i-- {
		c := bs[i]

		if c == '-' {
			if segLen == 0 || !segHasDigit {
				break
			}
			if bs[i+1]-'0' < 10 {
				verStart = i
			}
			segHasDigit, segLen = false, 0
			continue
		}

		if c-'0' < 10 {
			segHasDigit = true
		} else if c != '.' && c != '+' && !isAlpha(c) {
			break
		}
		segLen++
	}

	if verStart == 0 {
		return 0, 0
	}

	return verStart, end
}

// spansSIMD is the archsimd variant of Spans. Script remains scalar: its
// backward substring searches terminate within a few bytes on typical names,
// leaving no run for vector classification to shorten.
func spansSIMD(bs []byte) (j, s, e int) {
	length := len(bs)

	if r := sonameSIMD(bs); r > 0 {
		return r, 0, 0
	}

	if r1, r2 := Script(bs); r1 > 0 {
		return r1, r2, length
	}

	if r1, r2 := embeddedSIMD(bs); r1 > 0 {
		return r1, r2, length
	}

	if r1, r2 := suffixSIMD(bs); r1 > 0 {
		if r2 == length {
			return r1, 0, 0
		}
		return r1, r2, length
	}

	return length, 0, 0
}

// unmarkedSIMD collects the zero-bit indices below n from a plain word view
// of the claim bitset, skipping fully-marked 128-bit blocks with a vector
// test before falling back to per-word bit walks.
func unmarkedSIMD(words []uint64, n int, out []uint32) []uint32 {
	out = out[:0]
	i := 0
	for ; i+2 <= len(words); i += 2 {
		if blockAllOnes(archsimd.LoadUint64x2(words[i : i+2])) {
			continue
		}
		out = appendUnmarkedWord(out, words[i], i, n)
		out = appendUnmarkedWord(out, words[i+1], i+1, n)
	}
	for ; i < len(words); i++ {
		out = appendUnmarkedWord(out, words[i], i, n)
	}
	return out
}

// appendUnmarkedWord appends the zero-bit indices of word wi that fall below n.
func appendUnmarkedWord(out []uint32, w uint64, wi, n int) []uint32 {
	m := ^w
	base := uint32(wi << 6) // #nosec G115 -- callers bound the bitset size
	for m != 0 {
		idx := base + uint32(bits.TrailingZeros64(m))
		if int(idx) >= n {
			return out
		}
		out = append(out, idx)
		m &= m - 1
	}
	return out
}

// applyFlagsSIMD computes idKeys[i] = ids[i] &^ flag and
// exKeys[i] = exs[i] | flag, two lanes at a time. It is the vector form of
// the key-derivation ops in the bucket-build loop.
func applyFlagsSIMD(idKeys, exKeys, ids, exs []uint64, flag uint64) {
	f := archsimd.BroadcastUint64x2(flag)
	i := 0
	for ; i+2 <= len(ids); i += 2 {
		archsimd.LoadUint64x2(ids[i : i+2]).AndNot(f).Store(idKeys[i : i+2])
		archsimd.LoadUint64x2(exs[i : i+2]).Or(f).Store(exKeys[i : i+2])
	}
	for ; i < len(ids); i++ {
		idKeys[i] = ids[i] &^ flag
		exKeys[i] = exs[i] | flag
	}
}
