package files

import (
	"fmt"
	"hash/maphash"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/egibs/reconcile/internal/identity"
	"go.uber.org/goleak"
)

// testHash is a helper for tests that need to call hash directly.
func testHash(s string) (uint64, uint64) {
	return identity.Hash(s, seed)
}

func TestDiff_Basic(t *testing.T) {
	old := []string{"lib.so.1", "bin/foo", "doc.md", "old.txt"}
	cur := []string{"lib.so.2", "bin/foo", "doc.md", "new.txt"}

	r := Diff(old, cur)

	want := [4]uint32{2, 1, 1, 1} // Unchanged, Updated, Removed, Added
	got := [4]uint32{r.Count(Unchanged), r.Count(Updated), r.Count(Removed), r.Count(Added)}
	if got != want {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestDiff_Concurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := make([]string, 1000)
		cur := make([]string, 1000)
		for i := range 1000 {
			old[i] = fmt.Sprintf("lib/foo%d.so.1.0.0", i)
			cur[i] = fmt.Sprintf("lib/foo%d.so.1.1.0", i)
		}

		r := diffP(old, cur, 4)
		if r.Count(Updated) != 1000 {
			t.Errorf("updated = %d, want 1000", r.Count(Updated))
		}
	})
}

func TestHash_SameIdentity(t *testing.T) {
	cases := [][2]string{
		{"libfoo.so.1.0.0", "libfoo.so.2.0.0"},
		{"app-1.0.0-r0", "app-2.0.0-r5"},
		{"foo.1.2.3.so", "foo.4.5.6.so"},
		{"binary", "binary"},
		{"file-1.txt", "file-2.txt"},
	}

	for _, c := range cases {
		h1, _ := testHash(c[0])
		h2, _ := testHash(c[1])
		if h1 != h2 {
			t.Errorf("hash mismatch: %q=%x, %q=%x", c[0], h1, c[1], h2)
		}
	}
}

func TestHash_DifferentIdentity(t *testing.T) {
	cases := [][2]string{
		{"libfoo.so.1", "libbar.so.1"},
		{"app-1.0.0", "other-1.0.0"},
		{"a.txt", "b.txt"},
		{"file-1.txt", "file-2.pdf"},
		{"openssl-3", "openssl-3-doc"},
		// Regression: with an XOR span combiner these both collapsed to
		// identity hash 0 because their prefix and suffix spans are equal.
		{".config.1.2.3.config", ".cache.9.9.9.cache"},
	}

	for _, c := range cases {
		h1, _ := testHash(c[0])
		h2, _ := testHash(c[1])
		if h1 == h2 {
			t.Errorf("unexpected collision: %q and %q", c[0], c[1])
		}
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"libfoo.so.1.2.3", "libfoo.so.2.0.0", true},
		{"libfoo.so.1", "libbar.so.1", false},
		{"foo.1.2.3.so", "foo.4.5.6.so", true},
		{"app-1.0.0-r5", "app-2.0.0-r0", true},
		{"README.md", "README.md", true},
		{"a.txt", "b.txt", false},
		{"file-1.txt", "file-2.txt", true},         // same extension, same identity
		{"file-1.txt", "file-2.pdf", false},        // different extensions never share identity
		{"libfoo.so.1", "libfoo.so.1.conf", false}, // library vs. config file
		{"openssl-3", "openssl-3-doc", false},      // subpackage is not a version bump
	}

	for _, c := range cases {
		if got := identity.Equal(c.a, c.b); got != c.want {
			t.Errorf("Equal(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDiff_Determinism(t *testing.T) {
	old := []string{"c.so.1", "a.so.1", "b.so.1"}
	cur := []string{"c.so.2", "a.so.2", "b.so.2"}

	first := Diff(old, cur)
	for range 10 {
		run := Diff(old, cur)
		if !slices.Equal(first.E, run.E) {
			t.Fatal("non-deterministic")
		}
	}
}

func TestDiffRace2Determinism(t *testing.T) {
	// Two identical old files competing for the single matching cur file.
	// The lower old-file index must always win, producing deterministic output.
	old := []string{"foo", "foo"}
	cur := []string{"foo"}

	first := diffP(old, cur, 4)
	for range 100 {
		run := diffP(old, cur, 4)
		if !slices.Equal(first.E, run.E) {
			t.Fatal("non-deterministic: parallel reconciliation race detected")
		}
	}
}

func TestDiffRace1DuplicateCur(t *testing.T) {
	// Single old file with two identical cur candidates.
	// The shard map min-index fix ensures the lowest cur index wins deterministically.
	old := []string{"a-1.0"}
	cur := []string{"a-2.0", "a-2.0"}

	first := diffP(old, cur, 4)
	for range 100 {
		run := diffP(old, cur, 4)
		if !slices.Equal(first.E, run.E) {
			t.Fatal("non-deterministic: shard map race detected")
		}
	}
}

func TestDiff_WorkerInvariance(t *testing.T) {
	// Results must be identical across worker counts: the sequential and
	// parallel reconcilers implement the same per-key pairing.
	const n = 20_000
	old := make([]string, 0, n)
	cur := make([]string, 0, n)
	for i := range n / 4 {
		old = append(old,
			fmt.Sprintf("libx%d.so.1", i%997),      // identity groups with duplicates
			fmt.Sprintf("libx%d.so.1.0.0", i%997),  // soname pairs
			fmt.Sprintf("app%d-1.0.0-r%d", i, i%3), // suffix versions
			fmt.Sprintf("static-%d.txt", i%1500),   // exact duplicates
		)
		cur = append(cur,
			fmt.Sprintf("libx%d.so.2", i%997),
			fmt.Sprintf("libx%d.so.2.0.0", i%997),
			fmt.Sprintf("app%d-2.0.0-r%d", i, i%5),
			fmt.Sprintf("static-%d.txt", i%1500),
		)
	}

	want := diffP(old, cur, 1)
	for _, w := range []int{2, 4, 8, 16} {
		got := diffP(old, cur, w)
		if !slices.Equal(want.E, got.E) {
			t.Fatalf("workers=%d entries differ from workers=1", w)
		}
		if want.C != got.C {
			t.Fatalf("workers=%d counts %v differ from workers=1 %v", w, got.C, want.C)
		}
	}
}

// counts returns the four status counts of a Result for compact assertions.
func counts(r *Result) [4]uint32 {
	return [4]uint32{r.Count(Unchanged), r.Count(Updated), r.Count(Removed), r.Count(Added)}
}

func TestDiff_SonamePair(t *testing.T) {
	// The standard shared-library layout: a version symlink plus the real
	// file, across a version bump. Both pairs share identity "libfoo.so"
	// and both must reconcile as Updated.
	old := []string{"libfoo.so.1", "libfoo.so.1.0.0"}
	cur := []string{"libfoo.so.2", "libfoo.so.2.0.0"}

	r := Diff(old, cur)
	if got, want := counts(r), [4]uint32{0, 2, 0, 0}; got != want {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestDiff_ExactBeatsIdentity(t *testing.T) {
	// "libfoo.so.2" is byte-identical in both lists and must classify as
	// Unchanged even though "libfoo.so.1" shares its identity and is
	// processed first.
	old := []string{"libfoo.so.1", "libfoo.so.2"}
	cur := []string{"libfoo.so.2", "libfoo.so.3"}

	r := Diff(old, cur)
	if got, want := counts(r), [4]uint32{1, 1, 0, 0}; got != want {
		t.Errorf("counts = %v, want %v", got, want)
	}
	if !slices.Contains(r.E, Entry{1, 0, Unchanged}) {
		t.Errorf("missing Unchanged entry for libfoo.so.2: %v", r.E)
	}
	if !slices.Contains(r.E, Entry{0, 1, Updated}) {
		t.Errorf("missing Updated entry libfoo.so.1 -> libfoo.so.3: %v", r.E)
	}

	// Same shape with suffix-versioned names, shrinking to one cur file.
	r = Diff([]string{"a-1.0", "a-2.0"}, []string{"a-2.0"})
	if got, want := counts(r), [4]uint32{1, 0, 1, 0}; got != want {
		t.Errorf("suffix chain counts = %v, want %v", got, want)
	}
}

func TestDiff_IdenticalDuplicates(t *testing.T) {
	cases := []struct {
		name     string
		old, cur []string
		want     [4]uint32
	}{
		{"balanced", []string{"a", "a"}, []string{"a", "a"}, [4]uint32{2, 0, 0, 0}},
		{"more_old", []string{"a", "a"}, []string{"a"}, [4]uint32{1, 0, 1, 0}},
		{"more_cur", []string{"a"}, []string{"a", "a"}, [4]uint32{1, 0, 0, 1}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := Diff(tt.old, tt.cur)
			if got := counts(r); got != tt.want {
				t.Errorf("counts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiff_ExtensionContract(t *testing.T) {
	// Files of the same name with different extensions do not share an
	// identity (README contract).
	cases := [][2][]string{
		{{"file-1.txt"}, {"file-2.pdf"}},
		{{"report-2024.pdf"}, {"report-2025.txt"}},
		{{"libfoo.so.1"}, {"libfoo.so.1.conf"}},
	}
	for _, c := range cases {
		r := Diff(c[0], c[1])
		if got, want := counts(r), [4]uint32{0, 0, 1, 1}; got != want {
			t.Errorf("Diff(%v, %v) counts = %v, want %v", c[0], c[1], got, want)
		}
	}
}

func TestDiff_NoFalseVersionPatterns(t *testing.T) {
	// Two-component embedded versions and digit-free trailing segments are
	// not version patterns; these pairs are distinct files.
	cases := [][2][]string{
		{{"data.1.2.json"}, {"data.9.9.json"}},
		{{"openssl-3"}, {"openssl-3-doc"}},
	}
	for _, c := range cases {
		r := Diff(c[0], c[1])
		if got, want := counts(r), [4]uint32{0, 0, 1, 1}; got != want {
			t.Errorf("Diff(%v, %v) counts = %v, want %v", c[0], c[1], got, want)
		}
	}
}

func TestDiff_EqualSpanIdentities(t *testing.T) {
	// Regression: names whose prefix and suffix spans are byte-equal used to
	// collapse to identity hash 0, shadowing genuine matches.
	old := []string{".config.1.2.3.config"}
	cur := []string{".cache.9.9.9.cache", ".config.9.9.9.config"}

	r := Diff(old, cur)
	if got, want := counts(r), [4]uint32{0, 1, 0, 1}; got != want {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestDiff_Empty(t *testing.T) {
	r := Diff(nil, nil)
	got := [4]uint32{r.Count(Unchanged), r.Count(Updated), r.Count(Removed), r.Count(Added)}
	if got != [4]uint32{} {
		t.Errorf("expected zero counts, got %v", got)
	}
}

func TestDiff_LargeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large test")
	}

	const n = 100_000
	old := make([]string, n)
	cur := make([]string, n)

	for i := range n {
		old[i] = fmt.Sprintf("lib/libfoo%d.so.1.0.0", i)
		cur[i] = fmt.Sprintf("lib/libfoo%d.so.1.1.0", i)
	}
	// 10% unchanged
	for i := range n / 10 {
		cur[i] = old[i]
	}
	// 1% removed/added
	for i := n - n/100; i < n; i++ {
		old[i] = fmt.Sprintf("old/rm%d.so.1", i)
		cur[i] = fmt.Sprintf("cur/add%d.so.1", i)
	}

	r := Diff(old, cur)

	t.Logf("100k: unchanged=%d updated=%d removed=%d added=%d",
		r.Count(Unchanged), r.Count(Updated), r.Count(Removed), r.Count(Added))

	if r.Count(Unchanged) < n/10-n/100 {
		t.Errorf("expected ~%d unchanged", n/10)
	}
}

func BenchmarkHash(b *testing.B) {
	paths := []string{
		"libfoo.so.1.2.3",
		"app-1.0.0-r5",
		"foo.1.2.3.so",
		"README.md",
	}

	for b.Loop() {
		for _, p := range paths {
			identity.Hash(p, seed)
		}
	}
}

func BenchmarkDiff1K(b *testing.B)   { benchDiff(b, 1_000) }
func BenchmarkDiff10K(b *testing.B)  { benchDiff(b, 10_000) }
func BenchmarkDiff100K(b *testing.B) { benchDiff(b, 100_000) }
func BenchmarkDiff1M(b *testing.B)   { benchDiff(b, 1_000_000) }
func BenchmarkDiff10M(b *testing.B)  { benchDiff(b, 10_000_000) }

func benchDiff(b *testing.B, n int) {
	b.Helper()
	old, cur := genData(n)
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		Diff(old, cur)
	}
}

func BenchmarkDiff1M_Workers(b *testing.B) {
	old, cur := genData(1_000_000)
	for _, w := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("w=%d", w), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				diffP(old, cur, w)
			}
		})
	}
}

func BenchmarkMemory1M(b *testing.B) {
	old, cur := genData(1_000_000)

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	var r *Result
	iters := 0
	for b.Loop() {
		r = Diff(old, cur)
		iters++
	}

	runtime.ReadMemStats(&m2)
	_ = r

	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc)/1e6/float64(iters), "MB-alloc/op")
}

func genData(n int) ([]string, []string) {
	old := make([]string, n)
	cur := make([]string, n)
	for i := range n {
		old[i] = fmt.Sprintf("lib/libfoo%d.so.1.0.0", i)
		cur[i] = fmt.Sprintf("lib/libfoo%d.so.1.1.0", i)
	}
	return old, cur
}

// Benchmarks for individual pattern matchers

func BenchmarkSoname(b *testing.B) {
	paths := [][]byte{
		[]byte("lib/libcrypto.so.1.1.0"),
		[]byte("lib/libssl.so.3"),
		[]byte("usr/lib/x86_64-linux-gnu/libstdc++.so.6.0.30"),
		[]byte("usr/bin/ls"), // no match
	}
	for b.Loop() {
		for _, p := range paths {
			identity.Soname(p)
		}
	}
}

func BenchmarkScript(b *testing.B) {
	paths := [][]byte{
		[]byte("alpine-baselayout-3.6.8-r1.Q17OteNVXn9iSXcJI1Vf8x0TVc9Y.post-install"),
		[]byte("busybox-1.37.0-r12.Q1sSNCl4MTQ0d1V0NTXAhIjY7Nqo.trigger"),
		[]byte("ca-certificates-20250619-r0.Q1xUNRT2WUrGiLIMFZ1e2JbKz6MQ.post-deinstall"),
		[]byte("usr/bin/ls"), // no match
	}
	for b.Loop() {
		for _, p := range paths {
			identity.Script(p)
		}
	}
}

func BenchmarkSuffix(b *testing.B) {
	paths := [][]byte{
		[]byte("app-1.0.0-r5"),
		[]byte("tool-2.3.4-beta1"),
		[]byte("python3.11-pip-24.0"),
		[]byte("usr/bin/ls"), // no match
	}
	for b.Loop() {
		for _, p := range paths {
			identity.Suffix(p)
		}
	}
}

func BenchmarkEmbedded(b *testing.B) {
	paths := [][]byte{
		[]byte("foo.1.2.3.so"),
		[]byte("bar.4.5.6.dylib"),
		[]byte("libfoo.1.2.3.4.so"),
		[]byte("usr/bin/ls"), // no match
	}
	for b.Loop() {
		for _, p := range paths {
			identity.Embedded(p)
		}
	}
}

func BenchmarkSpans(b *testing.B) {
	paths := [][]byte{
		[]byte("lib/libcrypto.so.1.1.0"),
		[]byte("alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install"),
		[]byte("app-1.0.0-r5"),
		[]byte("foo.1.2.3.so"),
		[]byte("usr/bin/ls"),
	}
	for b.Loop() {
		for _, p := range paths {
			identity.Spans(p)
		}
	}
}

func BenchmarkEqual(b *testing.B) {
	pairs := [][2]string{
		{"lib/libcrypto.so.1.1.0", "lib/libcrypto.so.3.0.0"},
		{"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install", "alpine-baselayout-3.7.0-r0.Q1KfmXSO6h.post-install"},
		{"app-1.0.0-r5", "app-2.0.0-r0"},
		{"usr/bin/ls", "usr/bin/ls"},
	}
	for b.Loop() {
		for _, p := range pairs {
			identity.Equal(p[0], p[1])
		}
	}
}

// Additional pattern matcher tests.
func TestSoname(t *testing.T) {
	tests := []struct {
		input string
		want  int // position of the '.' before version number
	}{
		{"libfoo.so.1", 9},                 // identity: libfoo.so
		{"libfoo.so.1.2.3", 9},             // identity: libfoo.so
		{"libz.so.1", 7},                   // identity: libz.so
		{"libssl.so.3", 9},                 // identity: libssl.so
		{"usr/lib/libcrypto.so.1.1.0", 20}, // identity: usr/lib/libcrypto.so
		{"libfoo.so", 0},                   // no version
		{"foo.txt", 0},                     // not a .so
		{".so.1", 3},                       // minimal match (edge case)
		{"libfoo.so.1.conf", 0},            // version must end the name
		{"libfoo.so.1.txt", 0},             // version must end the name
		{"x.conf.so.2", 9},                 // trailing version after interior dots
		{"libfoo.so.", 0},                  // no digit after separator
		{"libfoo.so.a", 0},                 // non-numeric version
	}

	for _, tt := range tests {
		got := identity.Soname([]byte(tt.input))
		if got != tt.want {
			t.Errorf("Soname(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestScript(t *testing.T) {
	tests := []struct {
		input      string
		wantPkgEnd int // position of '-' before version
		wantSuffix int // position of '.' before script suffix
	}{
		// Test actual values by computing: len - len(suffix), find -digit
		{"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install", 17, 38},
		{"busybox-1.37.0-r12.Q1sSNCl4MTQ0.trigger", 7, 31},
		{"foo-1.0.Q1xxx.pre-install", 3, 13},
		{"foo-1.0.Q1xxx.pre-upgrade", 3, 13},
		{"foo-1.0.Q1xxx.post-upgrade", 3, 13},
		{"usr/bin/ls", 0, 0},        // no match
		{"foo.post-install", 0, 0},  // no .Q1
		{"foo-1.0.Q1xxx.txt", 0, 0}, // wrong suffix
	}

	for _, tt := range tests {
		gotPkg, gotSuffix := identity.Script([]byte(tt.input))
		if gotPkg != tt.wantPkgEnd || gotSuffix != tt.wantSuffix {
			t.Errorf("Script(%q) = (%d, %d), want (%d, %d)",
				tt.input, gotPkg, gotSuffix, tt.wantPkgEnd, tt.wantSuffix)
		}
	}
}

func TestSuffix(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantExt int
	}{
		{"app-1.0.0", 3, 9},
		{"app-1.0.0-r5", 3, 12},
		{"tool-2.3.4-beta1", 4, 16},
		{"python-3.11", 6, 11},
		{"file-1.txt", 4, 6},        // extension preserved in identity
		{"report-2024.pdf", 6, 11},  // extension preserved in identity
		{"libfoo-1.0.so", 6, 10},    // extension preserved in identity
		{"usr/bin/ls", 0, 0},        // no version suffix
		{"foo", 0, 0},               // too short
		{"foo-bar", 0, 0},           // no digit after -
		{"openssl-3-doc", 0, 0},     // trailing subpackage name is not a version
		{"gtk-3-demo", 0, 0},        // trailing subpackage name is not a version
		{"config-2fa-backup", 0, 0}, // trailing subpackage name is not a version
		{"-1.0", 0, 0},              // empty name
	}

	for _, tt := range tests {
		got, gotExt := identity.Suffix([]byte(tt.input))
		if got != tt.want || gotExt != tt.wantExt {
			t.Errorf("Suffix(%q) = (%d, %d), want (%d, %d)", tt.input, got, gotExt, tt.want, tt.wantExt)
		}
	}
}

func TestEmbedded(t *testing.T) {
	tests := []struct {
		input string
		wantI int
		wantJ int
	}{
		{"foo.1.2.3.so", 3, 9},
		{"bar.4.5.6.dylib", 3, 9},
		{"libfoo.1.2.3.4.so", 6, 14},
		{"foo.so", 0, 0},             // no embedded version
		{"foo.1.so", 0, 0},           // one-component version
		{"foo.1.2.so", 0, 0},         // two-component version
		{"data.1.2.json", 0, 0},      // two-component version
		{"report.2024.12.pdf", 0, 0}, // date-style name, not a version
		{"foo.txt", 0, 0},            // not a library
	}

	for _, tt := range tests {
		gotI, gotJ := identity.Embedded([]byte(tt.input))
		if gotI != tt.wantI || gotJ != tt.wantJ {
			t.Errorf("Embedded(%q) = (%d, %d), want (%d, %d)",
				tt.input, gotI, gotJ, tt.wantI, tt.wantJ)
		}
	}
}

func TestDiffWith_Default(t *testing.T) {
	cases := []struct {
		name     string
		old, cur []string
	}{
		{"empty", nil, nil},
		{"basic", []string{"lib.so.1", "bin/foo"}, []string{"lib.so.2", "bin/foo"}},
		{"all_removed", []string{"a.so.1", "b.so.1"}, nil},
		{"all_added", nil, []string{"c.so.1", "d.so.1"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := DiffWith(tt.old, tt.cur)
			want := Diff(tt.old, tt.cur)
			if !slices.Equal(got.E, want.E) {
				t.Errorf("DiffWith() entries != Diff() entries: got %v, want %v", got.E, want.E)
			}
		})
	}
}

func TestDiffWith_ExactOnly(t *testing.T) {
	// Hash function returns (exact, exact): disables identity matching.
	exactOnly := func(s string, seed maphash.Seed) (uint64, uint64) {
		_, exact := identity.Hash(s, seed)
		return exact, exact
	}
	// Equal function always rejects identity matches (they can't reach this since identity == exact).
	neverEqual := func(_, _ string) bool { return false }

	old := []string{"lib.so.1", "exact.txt"}
	cur := []string{"lib.so.2", "exact.txt"}

	r := DiffWith(old, cur, WithIdentity(exactOnly, neverEqual))

	// lib.so.1 and lib.so.2 have different exact hashes: no match → Removed + Added.
	// exact.txt and exact.txt share the same exact hash → Unchanged.
	if r.Count(Unchanged) != 1 {
		t.Errorf("Unchanged = %d, want 1", r.Count(Unchanged))
	}
	if r.Count(Updated) != 0 {
		t.Errorf("Updated = %d, want 0", r.Count(Updated))
	}
	if r.Count(Removed) != 1 {
		t.Errorf("Removed = %d, want 1", r.Count(Removed))
	}
	if r.Count(Added) != 1 {
		t.Errorf("Added = %d, want 1", r.Count(Added))
	}
}

func TestDiffWith_NilHashFunc(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic with nil HashFunc")
		}
	}()
	DiffWith([]string{"a"}, []string{"b"}, WithIdentity(nil, identity.Equal))
}

func TestDiffWith_NilEqualFunc(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic with nil EqualFunc")
		}
	}()
	DiffWith([]string{"a"}, []string{"b"}, WithIdentity(identity.Hash, nil))
}

func TestDiffWith_ExactFlagIgnored(t *testing.T) {
	// A HashFunc that sets bit 63 on every hash must not corrupt the shared
	// key namespace: DiffWith masks the flag at key construction, so results
	// are identical to the well-behaved equivalent.
	flagged := func(s string, seed maphash.Seed) (uint64, uint64) {
		h, e := identity.Hash(s, seed)
		return h | identity.ExactFlag, e | identity.ExactFlag
	}

	old := []string{"A", "a-1.0"}
	cur := []string{"A", "a-2.0", "B"}

	r := DiffWith(old, cur, WithIdentity(flagged, identity.Equal))

	want := [4]uint32{1, 1, 0, 1} // Unchanged, Updated, Removed, Added
	got := [4]uint32{r.Count(Unchanged), r.Count(Updated), r.Count(Removed), r.Count(Added)}
	if got != want {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestDiffWith_HashFuncNeverSeesSyntheticInput(t *testing.T) {
	// A HashFunc may assume it is only called with strings from the input
	// lists; one that indexes s[0] must not panic on a synthetic probe.
	indexing := func(s string, seed maphash.Seed) (uint64, uint64) {
		_ = s[0] // panics on ""
		return identity.Hash(s, seed)
	}

	r := DiffWith([]string{"foo"}, []string{"foo"}, WithIdentity(indexing, identity.Equal))
	if r.Count(Unchanged) != 1 {
		t.Errorf("Unchanged = %d, want 1", r.Count(Unchanged))
	}
}

func TestDiffWith_ExactEquality(t *testing.T) {
	// Normalized (case-insensitive) matching: the hash and both equality
	// functions operate on the folded form, so "README" and "readme"
	// classify as Unchanged.
	foldedHash := func(s string, seed maphash.Seed) (uint64, uint64) {
		return identity.Hash(strings.ToLower(s), seed)
	}

	r := DiffWith([]string{"README"}, []string{"readme"},
		WithIdentity(foldedHash, strings.EqualFold),
		WithExactEquality(strings.EqualFold))

	if r.Count(Unchanged) != 1 {
		t.Errorf("Unchanged = %d, want 1", r.Count(Unchanged))
	}
}

func TestDiffWith_NilExactFunc(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic with nil exact EqualFunc")
		}
	}()
	DiffWith([]string{"a"}, []string{"b"}, WithExactEquality(nil))
}

func TestDiffWith_CustomIdentity(t *testing.T) {
	// Equal function rejects all identity matches; only exact matches succeed.
	noIdentityEqual := func(_, _ string) bool { return false }

	old := []string{"lib.so.1", "static.txt"}
	cur := []string{"lib.so.2", "static.txt"}

	r := DiffWith(old, cur, WithIdentity(identity.Hash, noIdentityEqual))

	// lib.so.1 → lib.so.2: identity hashes match but equalFn rejects → Removed.
	// static.txt → static.txt: exact match (bypasses equalFn) → Unchanged.
	// lib.so.2: unmatched → Added.
	if r.Count(Unchanged) != 1 {
		t.Errorf("Unchanged = %d, want 1", r.Count(Unchanged))
	}
	if r.Count(Removed) != 1 {
		t.Errorf("Removed = %d, want 1", r.Count(Removed))
	}
	if r.Count(Added) != 1 {
		t.Errorf("Added = %d, want 1", r.Count(Added))
	}
	if r.Count(Updated) != 0 {
		t.Errorf("Updated = %d, want 0", r.Count(Updated))
	}
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
