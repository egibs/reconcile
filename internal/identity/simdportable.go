//go:build goexperiment.simd

package identity

import (
	"simd"
)

// This file contains portable (package simd) variants of the identity
// byte-scan kernels, used as the fallback where simd/archsimd lacks the
// target architecture. The portable mask types expose no bitmask conversion,
// so match positions are recovered by storing the mask lanes to a stack
// buffer and scanning it; that extraction cost is the main difference from
// the archsimd kernels.

// maxPortableLanes bounds the mask spill buffer; portable vectors are at
// most 512 bits (64 byte lanes).
const maxPortableLanes = 64

// digitOrDotVec returns a mask of lanes that are ASCII digits or '.'.
// The portable API has no unsigned Less, so v-'0' <= 9 is checked as
// min(v-'0', 9) == v-'0'.
func digitOrDotVec(v simd.Uint8s) simd.Mask8s {
	d := v.Sub(simd.BroadcastUint8s('0'))
	digit := d.Min(simd.BroadcastUint8s(9)).Equal(d)
	return digit.Or(v.Equal(simd.BroadcastUint8s('.')))
}

// tailScanVec is the portable form of tailScanSIMD: it returns the largest
// index t <= end such that bs[t] is neither an ASCII digit nor a dot, or -1
// when bs[0:end+1] is all digits and dots.
func tailScanVec(bs []byte, end int) int {
	lanes := simd.BroadcastUint8s(0).Len()
	var buf [maxPortableLanes]int8

	i := end + 1
	for i >= lanes {
		digitOrDotVec(simd.LoadUint8s(bs[i-lanes : i])).ToInt8s().Store(buf[:lanes])
		n := 0
		for n < lanes && buf[lanes-1-n] != 0 {
			n++
		}
		i -= n
		if n < lanes {
			return i - 1
		}
	}

	j := i - 1
	for j >= 0 && (bs[j]-'0' < 10 || bs[j] == '.') {
		j--
	}
	return j
}

// lastDotVec returns the index of the last '.' in bs, or -1 if none.
func lastDotVec(bs []byte) int {
	lanes := simd.BroadcastUint8s(0).Len()
	var buf [maxPortableLanes]int8

	i := len(bs)
	for i >= lanes {
		v := simd.LoadUint8s(bs[i-lanes : i])
		v.Equal(simd.BroadcastUint8s('.')).ToInt8s().Store(buf[:lanes])
		for p := lanes - 1; p >= 0; p-- {
			if buf[p] != 0 {
				return i - lanes + p
			}
		}
		i -= lanes
	}

	for j := i - 1; j >= 0; j-- {
		if bs[j] == '.' {
			return j
		}
	}
	return -1
}

// sonameVec is the portable-simd variant of Soname.
func sonameVec(bs []byte) int {
	i := len(bs) - 1
	if i < 4 || bs[i]-'0' >= 10 {
		return 0
	}

	i = tailScanVec(bs, i)

	if i >= 2 && bs[i] == 'o' && bs[i-1] == 's' && bs[i-2] == '.' && bs[i+1] == '.' {
		return i + 1
	}

	return 0
}

// applyFlagsVec is the portable-simd variant of applyFlagsSIMD.
func applyFlagsVec(idKeys, exKeys, ids, exs []uint64, flag uint64) {
	f := simd.BroadcastUint64s(flag)
	lanes := f.Len()
	i := 0
	for ; i+lanes <= len(ids); i += lanes {
		simd.LoadUint64s(ids[i : i+lanes]).AndNot(f).Store(idKeys[i : i+lanes])
		simd.LoadUint64s(exs[i : i+lanes]).Or(f).Store(exKeys[i : i+lanes])
	}
	for ; i < len(ids); i++ {
		idKeys[i] = ids[i] &^ flag
		exKeys[i] = exs[i] | flag
	}
}
