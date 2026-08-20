//go:build goexperiment.simd

package identity

import (
	"math/bits"
	"simd/archsimd"
)

// hasArchSIMD reports whether the archsimd kernels may run on this CPU.
// The 128-bit intrinsics compile to VEX encodings, so require AVX.
func hasArchSIMD() bool { return archsimd.X86.AVX() }

// runFromTop returns the number of contiguous matching lanes counting down
// from lane 15.
func runFromTop(m archsimd.Mask8x16) int {
	return bits.LeadingZeros16(^m.ToBits())
}

// lastMatch returns the index of the highest matching lane, or -1 if none.
func lastMatch(m archsimd.Mask8x16) int {
	b := m.ToBits()
	if b == 0 {
		return -1
	}
	return 15 - bits.LeadingZeros16(b)
}

// blockAllOnes reports whether both lanes are all-ones words.
func blockAllOnes(v archsimd.Uint64x2) bool {
	return v.Equal(archsimd.BroadcastUint64x2(^uint64(0))).ToBits() == 3
}

// countTop returns the number of matching lanes among the top n lanes.
func countTop(m archsimd.Mask8x16, n int) int {
	return bits.OnesCount16(m.ToBits() >> (16 - n))
}
