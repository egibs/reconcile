//go:build goexperiment.simd && (amd64 || arm64)

package identity

import (
	"bytes"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"sync/atomic"
	"testing"
)

// simdCorpus mirrors the pkg/files benchmark corpora plus boundary and
// long-tail cases that exercise the 16-byte chunk loop.
var simdCorpus = [][]byte{
	[]byte(""),
	[]byte("a"),
	[]byte(".so.1"),
	[]byte("usr/bin/ls"),
	[]byte("lib/libcrypto.so.1.1.0"),
	[]byte("lib/libssl.so.3"),
	[]byte("usr/lib/x86_64-linux-gnu/libstdc++.so.6.0.30"),
	[]byte("libfoo.so"),
	[]byte("libfoo.so.1.conf"),
	[]byte("foo.1.2.3.so"),
	[]byte("bar.4.5.6.dylib"),
	[]byte("libfoo.1.2.3.4.so"),
	[]byte("report.2024.12.pdf"),
	[]byte("app-1.0.0-r5"),
	[]byte("tool-2.3.4-beta1"),
	[]byte("python3.11-pip-24.0"),
	[]byte("openssl-3-doc"),
	[]byte("file-1.txt"),
	[]byte("alpine-baselayout-3.6.8-r1.Q17OteNVXn9iSXcJI1Vf8x0TVc9Y.post-install"),
	[]byte("busybox-1.37.0-r12.Q1sSNCl4MTQ0d1V0NTXAhIjY7Nqo.trigger"),
	// Version tails at and around the 16-byte chunk boundary.
	[]byte("lib.so.111.22222222"),                           // 12-byte tail
	[]byte("lib.so.111.222.3333333"),                        // 15-byte tail
	[]byte("lib.so.111.222.33333333"),                       // 16-byte tail
	[]byte("lib.so.111.222.333333333"),                      // 17-byte tail
	[]byte("libbig.so.1.2.3.4.5.6.7.8.9.10.11.12.13.14.15"), // 35-byte tail
	[]byte("1.2.3.4.5.6.7.8.9.10.11.12"),                    // all version bytes
	[]byte("usr/share/doc/some/deeply/nested/path/README"),
	[]byte("usr/share/doc/some/deeply/nested/path/with/no/dots/at-all"),
}

// randomNames returns deterministic pseudo-random names biased toward
// digits, dots, and dashes so the scan kernels see long mixed runs.
func randomNames(n int) [][]byte {
	rng := rand.New(rand.NewPCG(0xdead, 0xbeef))
	alphabet := []byte("abcdefgsoQ10123456789....----//_+")
	names := make([][]byte, n)
	for i := range names {
		b := make([]byte, rng.IntN(81))
		for j := range b {
			b[j] = alphabet[rng.IntN(len(alphabet))]
		}
		names[i] = b
	}
	return names
}

// tailScanRef is the scalar reference for the digit-and-dot backward scan.
func tailScanRef(bs []byte, end int) int {
	j := end
	for j >= 0 && (bs[j]-'0' < 10 || bs[j] == '.') {
		j--
	}
	return j
}

func requireArchSIMD(tb testing.TB) {
	tb.Helper()
	if !hasArchSIMD() {
		tb.Skip("archsimd kernels unsupported on this CPU")
	}
}

func TestSIMDKernelsMatchScalar(t *testing.T) {
	requireArchSIMD(t)

	inputs := append(simdCorpus, randomNames(5000)...)

	for _, bs := range inputs {
		name := string(bs)

		if len(bs) > 0 {
			end := len(bs) - 1
			if got, want := tailScanSIMD(bs, end), tailScanRef(bs, end); got != want {
				t.Errorf("tailScanSIMD(%q) = %d, want %d", name, got, want)
			}
		}

		if got, want := lastDotSIMD(bs), bytes.LastIndexByte(bs, '.'); got != want {
			t.Errorf("lastDotSIMD(%q) = %d, want %d", name, got, want)
		}

		if got, want := sonameSIMD(bs), Soname(bs); got != want {
			t.Errorf("sonameSIMD(%q) = %d, want %d", name, got, want)
		}

		g1, g2 := embeddedSIMD(bs)
		w1, w2 := Embedded(bs)
		if g1 != w1 || g2 != w2 {
			t.Errorf("embeddedSIMD(%q) = (%d, %d), want (%d, %d)", name, g1, g2, w1, w2)
		}

		g1, g2 = suffixSIMD(bs)
		w1, w2 = Suffix(bs)
		if g1 != w1 || g2 != w2 {
			t.Errorf("suffixSIMD(%q) = (%d, %d), want (%d, %d)", name, g1, g2, w1, w2)
		}

		gj, gs, ge := spansSIMD(bs)
		wj, ws, we := Spans(bs)
		if gj != wj || gs != ws || ge != we {
			t.Errorf("spansSIMD(%q) = (%d, %d, %d), want (%d, %d, %d)", name, gj, gs, ge, wj, ws, we)
		}
	}
}

func TestSIMDApplyFlagsMatchScalar(t *testing.T) {
	requireArchSIMD(t)

	for _, n := range []int{0, 1, 2, 3, 7, 64, 1023} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(n), 42)) // #nosec G115 -- test sizes are small non-negative constants
			ids, exs := make([]uint64, n), make([]uint64, n)
			for i := range ids {
				ids[i], exs[i] = rng.Uint64(), rng.Uint64()
			}

			wantID, wantEx := make([]uint64, n), make([]uint64, n)
			applyFlagsScalar(wantID, wantEx, ids, exs, ExactFlag)

			gotID, gotEx := make([]uint64, n), make([]uint64, n)
			applyFlagsSIMD(gotID, gotEx, ids, exs, ExactFlag)
			if !slicesEqual(gotID, wantID) || !slicesEqual(gotEx, wantEx) {
				t.Errorf("applyFlagsSIMD mismatch at n=%d", n)
			}

			clear(gotID)
			clear(gotEx)
			applyFlagsVec(gotID, gotEx, ids, exs, ExactFlag)
			if !slicesEqual(gotID, wantID) || !slicesEqual(gotEx, wantEx) {
				t.Errorf("applyFlagsVec mismatch at n=%d", n)
			}
		})
	}
}

func slicesEqual(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyFlagsScalar is the scalar baseline for the key-derivation kernel.
func applyFlagsScalar(idKeys, exKeys, ids, exs []uint64, flag uint64) {
	for i := range ids {
		idKeys[i] = ids[i] &^ flag
		exKeys[i] = exs[i] | flag
	}
}

// unmarkedPerBit mirrors the additions scan in diffPWith: one atomic load
// and bit test per index.
func unmarkedPerBit(v []atomic.Uint64, n int, out []uint32) []uint32 {
	out = out[:0]
	for i := range uint32(n) { // #nosec G115 -- bench sizes are bounded
		if !IsMarked(v, i) {
			out = append(out, i)
		}
	}
	return out
}

// unmarkedAtomicWords mirrors the additions scan adopted in diffPWith: one
// atomic load per word, zero bits walked inline with TrailingZeros64.
func unmarkedAtomicWords(v []atomic.Uint64, n uint32, out []uint32) []uint32 {
	out = out[:0]
	for wi := range v {
		m := ^v[wi].Load()
		base := uint32(wi) << 6 // #nosec G115 -- bench sizes are bounded
		for m != 0 {
			idx := base + uint32(bits.TrailingZeros64(m))
			if idx >= n {
				break
			}
			out = append(out, idx)
			m &= m - 1
		}
	}
	return out
}

// unmarkedWords scans a plain word view of the bitset, visiting each word
// once and walking zero bits with TrailingZeros64.
func unmarkedWords(words []uint64, n int, out []uint32) []uint32 {
	out = out[:0]
	for wi, w := range words {
		m := ^w
		base := uint32(wi << 6) // #nosec G115 -- bench sizes are bounded
		for m != 0 {
			idx := base + uint32(bits.TrailingZeros64(m))
			if int(idx) >= n {
				return out
			}
			out = append(out, idx)
			m &= m - 1
		}
	}
	return out
}

func TestSIMDUnmarkedScanVariantsMatch(t *testing.T) {
	requireArchSIMD(t)

	for _, density := range []float64{0, 0.1, 0.9, 1} {
		t.Run(fmt.Sprintf("marked=%.0f%%", density*100), func(t *testing.T) {
			const n = 10_000
			atomics, words := markedBitset(n, density)

			want := unmarkedPerBit(atomics, n, nil)
			if got := unmarkedAtomicWords(atomics, n, nil); !uint32sEqual(got, want) {
				t.Errorf("unmarkedAtomicWords mismatch: got %d indices, want %d", len(got), len(want))
			}
			if got := unmarkedWords(words, n, nil); !uint32sEqual(got, want) {
				t.Errorf("unmarkedWords mismatch: got %d indices, want %d", len(got), len(want))
			}
			if got := unmarkedSIMD(words, n, nil); !uint32sEqual(got, want) {
				t.Errorf("unmarkedSIMD mismatch: got %d indices, want %d", len(got), len(want))
			}
		})
	}
}

func uint32sEqual(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// markedBitset builds matching atomic and plain-word bitsets with the given
// fraction of the first n bits set.
func markedBitset(n int, density float64) ([]atomic.Uint64, []uint64) {
	rng := rand.New(rand.NewPCG(7, 11))
	atomics := make([]atomic.Uint64, (n+63)>>6)
	words := make([]uint64, len(atomics))
	for i := range n {
		if rng.Float64() < density {
			Mark(atomics, uint32(i)) // #nosec G115 -- bench sizes are bounded
			words[i>>6] |= 1 << (i & 63)
		}
	}
	return atomics, words
}

// Benchmarks: scalar baseline vs archsimd vs portable simd. Variants are
// ordered slices so benchmark output order is deterministic across runs.

type nameVariant struct {
	name string
	fn   func([]byte)
}

func benchNames(b *testing.B, name string, fns []nameVariant) {
	b.Helper()
	corpora := []struct {
		name  string
		paths [][]byte
	}{
		{"short", [][]byte{
			[]byte("lib/libcrypto.so.1.1.0"),
			[]byte("lib/libssl.so.3"),
			[]byte("usr/lib/x86_64-linux-gnu/libstdc++.so.6.0.30"),
			[]byte("usr/bin/ls"),
		}},
		{"long", [][]byte{
			[]byte("libbig.so.1.2.3.4.5.6.7.8.9.10.11.12.13.14.15"),
			[]byte("opt/homebrew/Cellar/openssl@3/3.3.1/lib/libcrypto.so.3.3.1"),
			[]byte("usr/share/doc/some/deeply/nested/path/with/no/dots/at-all"),
			[]byte("data.2024.10.11.12.13.14.15.16.17.18.19.20.csv"),
		}},
	}
	for _, corpus := range corpora {
		for _, variant := range fns {
			b.Run(fmt.Sprintf("%s/%s/%s", name, corpus.name, variant.name), func(b *testing.B) {
				for b.Loop() {
					for _, p := range corpus.paths {
						variant.fn(p)
					}
				}
			})
		}
	}
}

func BenchmarkSIMDSoname(b *testing.B) {
	requireArchSIMD(b)
	benchNames(b, "soname", []nameVariant{
		{"scalar", func(p []byte) { Soname(p) }},
		{"archsimd", func(p []byte) { sonameSIMD(p) }},
		{"portable", func(p []byte) { sonameVec(p) }},
	})
}

func BenchmarkSIMDEmbedded(b *testing.B) {
	requireArchSIMD(b)
	benchNames(b, "embedded", []nameVariant{
		{"scalar", func(p []byte) { Embedded(p) }},
		{"archsimd", func(p []byte) { embeddedSIMD(p) }},
	})
}

func BenchmarkSIMDSuffix(b *testing.B) {
	requireArchSIMD(b)
	benchNames(b, "suffix", []nameVariant{
		{"scalar", func(p []byte) { Suffix(p) }},
		{"archsimd", func(p []byte) { suffixSIMD(p) }},
	})
}

func BenchmarkSIMDSpans(b *testing.B) {
	requireArchSIMD(b)
	benchNames(b, "spans", []nameVariant{
		{"scalar", func(p []byte) { Spans(p) }},
		{"archsimd", func(p []byte) { spansSIMD(p) }},
	})
}

func BenchmarkSIMDLastDot(b *testing.B) {
	requireArchSIMD(b)
	benchNames(b, "lastdot", []nameVariant{
		{"scalar", func(p []byte) { bytes.LastIndexByte(p, '.') }},
		{"archsimd", func(p []byte) { lastDotSIMD(p) }},
		{"portable", func(p []byte) { lastDotVec(p) }},
	})
}

func BenchmarkSIMDApplyFlags(b *testing.B) {
	requireArchSIMD(b)

	const n = 4096
	rng := rand.New(rand.NewPCG(3, 5))
	ids, exs := make([]uint64, n), make([]uint64, n)
	for i := range ids {
		ids[i], exs[i] = rng.Uint64(), rng.Uint64()
	}
	dstID, dstEx := make([]uint64, n), make([]uint64, n)

	variants := []struct {
		name string
		fn   func()
	}{
		{"scalar", func() { applyFlagsScalar(dstID, dstEx, ids, exs, ExactFlag) }},
		{"archsimd", func() { applyFlagsSIMD(dstID, dstEx, ids, exs, ExactFlag) }},
		{"portable", func() { applyFlagsVec(dstID, dstEx, ids, exs, ExactFlag) }},
	}
	for _, variant := range variants {
		b.Run(variant.name, func(b *testing.B) {
			b.SetBytes(n * 8 * 2)
			for b.Loop() {
				variant.fn()
			}
		})
	}
}

func BenchmarkSIMDUnmarkedScan(b *testing.B) {
	requireArchSIMD(b)

	const n = 1 << 20
	for _, density := range []float64{0.1, 0.9} {
		atomics, words := markedBitset(n, density)
		out := make([]uint32, 0, n)

		variants := []struct {
			name string
			fn   func()
		}{
			{"perbit-atomic", func() { out = unmarkedPerBit(atomics, n, out) }},
			{"word-atomic", func() { out = unmarkedAtomicWords(atomics, n, out) }},
			{"word-scalar", func() { out = unmarkedWords(words, n, out) }},
			{"archsimd", func() { out = unmarkedSIMD(words, n, out) }},
		}
		for _, variant := range variants {
			b.Run(fmt.Sprintf("marked=%.0f%%/%s", density*100, variant.name), func(b *testing.B) {
				for b.Loop() {
					variant.fn()
				}
			})
		}
	}
}
