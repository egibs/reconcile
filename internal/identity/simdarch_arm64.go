//go:build goexperiment.simd

package identity

import (
	"math/bits"
	"simd/archsimd"
)

// hasArchSIMD reports whether the archsimd kernels may run on this CPU.
// Neon is baseline on arm64.
func hasArchSIMD() bool { return true }

// maskWords returns the mask as two uint64 words (lanes 0-7 in lo, 8-15 in
// hi) with each matching lane reading as 0xFF. Neon has no movemask, so
// position extraction works on the 0x00/0xFF byte pattern directly.
func maskWords(m archsimd.Mask8x16) (lo, hi uint64) {
	w := m.ToInt8x16().ToBits().ReshapeToUint64s()
	return w.GetElem(0), w.GetElem(1)
}

// runFromTop returns the number of contiguous matching lanes counting down
// from lane 15.
func runFromTop(m archsimd.Mask8x16) int {
	lo, hi := maskWords(m)
	n := bits.LeadingZeros64(^hi) >> 3
	if n == 8 {
		n += bits.LeadingZeros64(^lo) >> 3
	}
	return n
}

// lastMatch returns the index of the highest matching lane, or -1 if none.
func lastMatch(m archsimd.Mask8x16) int {
	lo, hi := maskWords(m)
	if hi != 0 {
		return 15 - (bits.LeadingZeros64(hi) >> 3)
	}
	if lo != 0 {
		return 7 - (bits.LeadingZeros64(lo) >> 3)
	}
	return -1
}

// blockAllOnes reports whether both lanes are all-ones words.
func blockAllOnes(v archsimd.Uint64x2) bool {
	return v.And(v.HiToLo()).GetElem(0) == ^uint64(0)
}

// countTop returns the number of matching lanes among the top n lanes.
func countTop(m archsimd.Mask8x16, n int) int {
	lo, hi := maskWords(m)
	if n <= 8 {
		return bits.OnesCount64(hi>>((8-n)*8)) >> 3
	}
	return (bits.OnesCount64(hi) + bits.OnesCount64(lo>>((16-n)*8))) >> 3
}
