//go:build goexperiment.simd

package identity

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// portableCorpus keeps this file self-contained on architectures where the
// archsimd test corpus is excluded by build tags.
var portableCorpus = [][]byte{
	[]byte(""),
	[]byte("a"),
	[]byte(".so.1"),
	[]byte("usr/bin/ls"),
	[]byte("lib/libcrypto.so.1.1.0"),
	[]byte("lib/libssl.so.3"),
	[]byte("usr/lib/x86_64-linux-gnu/libstdc++.so.6.0.30"),
	[]byte("libfoo.so"),
	[]byte("libfoo.so.1.conf"),
	[]byte("lib.so.111.222.33333333"),
	[]byte("libbig.so.1.2.3.4.5.6.7.8.9.10.11.12.13.14.15"),
	[]byte("1.2.3.4.5.6.7.8.9.10.11.12"),
	[]byte("usr/share/doc/some/deeply/nested/path/with/no/dots/at-all"),
}

func TestPortableKernelsMatchScalar(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xfeed, 0xface))
	alphabet := []byte("abcdefgsoQ10123456789....----//_+")

	inputs := portableCorpus
	for range 5000 {
		b := make([]byte, rng.IntN(81))
		for j := range b {
			b[j] = alphabet[rng.IntN(len(alphabet))]
		}
		inputs = append(inputs, b)
	}

	for _, bs := range inputs {
		name := string(bs)

		if len(bs) > 0 {
			end := len(bs) - 1
			want := end
			for want >= 0 && (bs[want]-'0' < 10 || bs[want] == '.') {
				want--
			}
			if got := tailScanVec(bs, end); got != want {
				t.Errorf("tailScanVec(%q) = %d, want %d", name, got, want)
			}
		}

		if got, want := lastDotVec(bs), bytes.LastIndexByte(bs, '.'); got != want {
			t.Errorf("lastDotVec(%q) = %d, want %d", name, got, want)
		}

		if got, want := sonameVec(bs), Soname(bs); got != want {
			t.Errorf("sonameVec(%q) = %d, want %d", name, got, want)
		}
	}
}
