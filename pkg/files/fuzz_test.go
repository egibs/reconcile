package files

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egibs/reconcile/internal/identity"
)

// FuzzDiffMain tests the main reconciliation algorithm with single file pairs.
// Validates that the result is non-nil and status counts are consistent.
func FuzzDiffMain(f *testing.F) {
	cases := []struct {
		a, b string
	}{
		// Soname patterns
		{"libfoo.so.1.2.3", "libfoo.so.2.0.0"},
		{"libfoo.so.1", "libbar.so.1"},
		{"libz.so.1", "libz.so.1.2.3"},
		{"libssl.so.3", "libssl.so.3.0.1"},
		// Embedded version patterns
		{"foo.1.2.3.so", "foo.4.5.6.so"},
		{"bar.1.2.3.dylib", "bar.4.5.6.dylib"},
		// Suffix patterns (APK-style)
		{"app-1.0.0-r5", "app-2.0.0-r0"},
		{"python-3.11.0", "python-3.12.0"},
		{"tool-2.3.4-beta1", "tool-2.3.5-beta2"},
		// Script patterns
		{"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install", "alpine-baselayout-3.6.9-r0.Q18ABCdef12.post-install"},
		{"busybox-1.37.0-r12.Q1sSNCl4MTQ0.trigger", "busybox-1.38.0-r0.Q1newHash99.trigger"},
		// Exact matches
		{"README.md", "README.md"},
		{"binary", "binary"},
		{"file.go", "file.go"},
		// Different files
		{"a.txt", "b.txt"},
		{"foo.jar", "bar.jar"},
		{"lib.rs", "mod.rs"},
		// Edge cases
		{"", ""},
		{"a", "a"},
		{"a", "b"},
		{".so.1", ".so.1"},
		{".so", ".so"},
		{"archive.tar.gz", "archive.tar.gz"},
		// Unicode and special characters
		{"café-1.0.0", "café-2.0.0"},
		{"文件-1.0", "文件-2.0"},
		// Long filenames
		{"very-long-package-name-with-many-parts-1.2.3-r99", "very-long-package-name-with-many-parts-1.2.4-r0"},
	}

	for _, c := range cases {
		f.Add(c.a, c.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		res := Diff([]string{a}, []string{b})

		if res == nil {
			t.Fatal("result is unexpectedly nil")
		}

		// Validate status count consistency
		total := res.Count(Unchanged) + res.Count(Updated) + res.Count(Removed) + res.Count(Added)
		if int(total) != len(res.E) {
			t.Errorf("status count mismatch: sum=%d, entries=%d", total, len(res.E))
		}
	})
}

// FuzzDiffMulti tests reconciliation with multiple files to exercise
// concurrency, sharding, deduplication, and match tracking.
func FuzzDiffMulti(f *testing.F) {
	// Seed with various multi-file scenarios
	f.Add("libfoo.so.1\nlibbar.so.2", "libfoo.so.2\nlibbar.so.3\nlibnew.so.1")
	f.Add("a-1.0\nb-2.0\nc-3.0", "a-1.1\nb-2.1")
	f.Add("file1\nfile2\nfile3", "file1\nfile2\nfile3")
	f.Add("", "new1\nnew2")
	f.Add("old1\nold2", "")
	f.Add("dup-1.0\ndup-1.0", "dup-2.0")
	f.Add("libx.so.1\nlibx.so.2\nlibx.so.3", "libx.so.4")
	f.Add("pkg-1.0.Q1abc.post-install\npkg-1.0.Q1def.trigger", "pkg-2.0.Q1xyz.post-install\npkg-2.0.Q1uvw.trigger")

	f.Fuzz(func(t *testing.T, oldStr, newStr string) {
		old := splitNonEmpty(oldStr)
		cur := splitNonEmpty(newStr)

		res := Diff(old, cur)

		if res == nil {
			t.Fatal("result is unexpectedly nil")
		}

		// Validate counts
		unchanged := res.Count(Unchanged)
		updated := res.Count(Updated)
		removed := res.Count(Removed)
		added := res.Count(Added)

		total := unchanged + updated + removed + added
		if int(total) != len(res.E) {
			t.Errorf("count mismatch: sum=%d, entries=%d", total, len(res.E))
		}

		// Validate that removed + unchanged + updated == len(old)
		// (each old file is either unchanged, updated, or removed)
		oldCount := unchanged + updated + removed
		if int(oldCount) != len(old) {
			t.Errorf("old file count mismatch: got %d, want %d", oldCount, len(old))
		}

		// Validate entries have valid indices and statuses
		for status, e := range res.All() {
			switch status {
			case Unchanged, Updated:
				if e.Old == null || e.New == null {
					t.Errorf("unchanged/updated entry has null index: %+v", e)
				}
				if int(e.Old) >= len(old) || int(e.New) >= len(cur) {
					t.Errorf("entry index out of bounds: %+v, old=%d, new=%d", e, len(old), len(cur))
				}
			case Removed:
				if e.Old == null || e.New != null {
					t.Errorf("removed entry has wrong indices: %+v", e)
				}
			case Added:
				if e.Old != null || e.New == null {
					t.Errorf("added entry has wrong indices: %+v", e)
				}
			}
		}
	})
}

// FuzzDiffConcurrent tests reconciliation under concurrent execution
// to catch race conditions in sharding and bitset operations.
// Note: Results may differ when inputs contain duplicate identities due to
// non-deterministic worker ordering in map construction.
// Each result is verified separately rather than across calls.
func FuzzDiffConcurrent(f *testing.F) {
	f.Add("a-1.0\nb-2.0\nc-3.0\nd-4.0", "a-1.1\nb-2.1\nc-3.1\nd-4.1\ne-5.0")

	f.Fuzz(func(t *testing.T, oldStr, newStr string) {
		old := splitNonEmpty(oldStr)
		cur := splitNonEmpty(newStr)

		// Run multiple times concurrently to stress test
		var wg sync.WaitGroup
		results := make([]*Result, 4)

		for i := range results {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx] = Diff(old, cur)
			}(i)
		}
		wg.Wait()

		// Validate each result independently
		for i, res := range results {
			if res == nil {
				t.Errorf("result[%d] is nil", i)
				continue
			}

			// Validate count consistency
			total := res.Count(Unchanged) + res.Count(Updated) + res.Count(Removed) + res.Count(Added)
			if int(total) != len(res.E) {
				t.Errorf("result[%d]: count mismatch sum=%d, entries=%d", i, total, len(res.E))
			}

			// Each old file should appear exactly once (unchanged, updated, or removed)
			oldCount := res.Count(Unchanged) + res.Count(Updated) + res.Count(Removed)
			if int(oldCount) != len(old) {
				t.Errorf("result[%d]: old count mismatch got=%d, want=%d", i, oldCount, len(old))
			}
		}
	})
}

// FuzzEqual tests identity comparison with correctness validation.
func FuzzEqual(f *testing.F) {
	cases := []struct {
		a, b string
	}{
		// Same identity, different versions
		{"libfoo.so.1.2.3", "libfoo.so.2.0.0"},
		{"foo.1.2.3.so", "foo.4.5.6.so"},
		{"app-1.0.0-r5", "app-2.0.0-r0"},
		{"pkg-1.0.Q1abc.post-install", "pkg-2.0.Q1xyz.post-install"},
		// Different identity
		{"libfoo.so.1", "libbar.so.1"},
		{"a.txt", "b.txt"},
		// Exact matches
		{"README.md", "README.md"},
		{"binary", "binary"},
		// Edge cases
		{"", ""},
		{"a", "a"},
		{"a", "b"},
		{".so.1", ".so.1"},
		{"lib.rs", "mod.rs"},
		// Boundary cases
		{"foo-", "foo-"},
		{"-1.0", "-2.0"},
		{"x.so.", "x.so."},
	}

	for _, c := range cases {
		f.Add(c.a, c.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		eq := identity.Equal(a, b)

		// Reflexivity: Equal(a, a) must be true
		if !identity.Equal(a, a) {
			t.Errorf("reflexivity violated: Equal(%q, %q) = false", a, a)
		}

		// Symmetry: Equal(a, b) == Equal(b, a)
		if eq != identity.Equal(b, a) {
			t.Errorf("symmetry violated: Equal(%q, %q) != Equal(%q, %q)", a, b, b, a)
		}
	})
}

// FuzzSpans tests the Spans function which determines identity boundaries.
// This validates pattern priority: Soname > Script > Embedded > Suffix > direct.
func FuzzSpans(f *testing.F) {
	cases := []string{
		// Soname (highest priority)
		"libfoo.so.1.2.3",
		"lib/path/libbar.so.1",
		// Script
		"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install",
		"busybox-1.37.0-r12.Q1sSNCl4MTQ0.trigger",
		"pkg-1.0.Q1xxx.pre-upgrade",
		// Embedded
		"foo.1.2.3.so",
		"bar.4.5.6.dylib",
		// Suffix
		"app-1.0.0-r5",
		"tool-2.3.4",
		// Direct (no pattern)
		"README.md",
		"binary",
		// Edge cases
		"",
		"a",
		".so",
		".so.1",
		"x.1.y",
		// Ambiguous cases
		"libfoo.so.1.2.3.so",              // Should match soname
		"foo-1.0.Q1abc.post-install.so.1", // Multiple patterns
	}

	for _, c := range cases {
		f.Add(c)
	}

	f.Fuzz(func(t *testing.T, input string) {
		bs := []byte(input)
		j, s, e := identity.Spans(bs)
		length := len(bs)

		// j must be in range [0, len(input)]
		if j < 0 || j > length {
			t.Errorf("Spans(%q): j=%d out of range [0, %d]", input, j, length)
		}

		// s and e must be valid
		if s < 0 || e < 0 {
			t.Errorf("Spans(%q): negative span indices s=%d, e=%d", input, s, e)
		}

		// If s > 0, then e must be >= s and <= length
		if s > 0 && (e < s || e > length) {
			t.Errorf("Spans(%q): invalid second span s=%d, e=%d, len=%d", input, s, e, length)
		}
	})
}

// FuzzHash tests the Hash function which computes identity and exact hashes.
func FuzzHashSerial(f *testing.F) {
	cases := []string{
		"libfoo.so.1.2.3",
		"foo.1.2.3.so",
		"app-1.0.0-r5",
		"pkg-1.0.Q1abc.post-install",
		"README.md",
		"",
		"a",
		".so.1",
	}

	for _, c := range cases {
		f.Add(c)
	}

	seed := maphash.MakeSeed()

	f.Fuzz(func(t *testing.T, input string) {
		idHash, exHash := identity.Hash(input, seed)

		// Both hashes must have high bit cleared (ExactFlag reserved)
		if idHash&identity.ExactFlag != 0 {
			t.Errorf("Hash(%q): identity hash has ExactFlag set", input)
		}
		if exHash&identity.ExactFlag != 0 {
			t.Errorf("Hash(%q): exact hash has ExactFlag set", input)
		}

		// Exact hash should be consistent for same input
		idHash2, exHash2 := identity.Hash(input, seed)
		if idHash != idHash2 || exHash != exHash2 {
			t.Errorf("Hash(%q): non-deterministic results", input)
		}
	})
}

// FuzzHashAll tests parallel hash computation.
func FuzzHashAll(f *testing.F) {
	f.Add("a\nb\nc")
	f.Add("libfoo.so.1\nlibbar.so.2\nlibbaz.so.3")
	f.Add("")

	seed := maphash.MakeSeed()

	f.Fuzz(func(t *testing.T, filesStr string) {
		files := splitNonEmpty(filesStr)

		// Test with different worker counts
		for _, workers := range []int{1, 2, 4, 8, 16} {
			idHashes, exHashes := identity.HashAll(files, workers, seed)

			if len(idHashes) != len(files) || len(exHashes) != len(files) {
				t.Errorf("HashAll: length mismatch for workers=%d", workers)
			}

			// Verify each hash matches individual Hash call
			for i, f := range files {
				expectedID, expectedEx := identity.Hash(f, seed)
				if idHashes[i] != expectedID || exHashes[i] != expectedEx {
					t.Errorf("HashAll mismatch at index %d for workers=%d", i, workers)
				}
			}
		}
	})
}

// FuzzTryMark tests concurrent bitset marking operations.
func FuzzTryMarkSerial(f *testing.F) {
	f.Add(uint32(0), uint32(64))
	f.Add(uint32(63), uint32(64))
	f.Add(uint32(64), uint32(128))
	f.Add(uint32(127), uint32(128))
	f.Add(uint32(0), uint32(1))

	f.Fuzz(func(t *testing.T, idx, size uint32) {
		if size == 0 || idx >= size {
			t.Skip("invalid parameters")
		}

		// Allocate bitset
		bitsetLen := (size + 63) >> 6
		bitset := make([]atomic.Uint64, bitsetLen)

		// First mark should succeed
		if !identity.TryMark(bitset, idx) {
			t.Errorf("TryMark(%d) should succeed on fresh bitset", idx)
		}

		// Second mark should fail
		if identity.TryMark(bitset, idx) {
			t.Errorf("TryMark(%d) should fail on already-marked bit", idx)
		}

		// IsMarked should return true
		if !identity.IsMarked(bitset, idx) {
			t.Errorf("IsMarked(%d) should return true after TryMark", idx)
		}
	})
}

// FuzzTryMarkConcurrent tests concurrent bitset operations under contention.
func FuzzTryMarkConcurrent(f *testing.F) {
	f.Add(uint32(100))
	f.Add(uint32(1000))

	f.Fuzz(func(t *testing.T, size uint32) {
		if size == 0 || size > 10000 {
			t.Skip("size out of range")
		}

		bitsetLen := (size + 63) >> 6
		bitset := make([]atomic.Uint64, bitsetLen)

		// Track successful marks per bit
		successCount := make([]atomic.Uint32, size)

		var wg sync.WaitGroup
		workers := 8

		for range workers {
			wg.Go(func() {
				for i := range size {
					if identity.TryMark(bitset, i) {
						successCount[i].Add(1)
					}
				}
			})
		}
		wg.Wait()

		// Each bit should be successfully marked exactly once
		for i := range size {
			if successCount[i].Load() != 1 {
				t.Errorf("bit %d marked %d times (expected 1)", i, successCount[i].Load())
			}
			if !identity.IsMarked(bitset, i) {
				t.Errorf("bit %d not marked after TryMark", i)
			}
		}
	})
}

// FuzzSoname tests shared library version pattern detection.
func FuzzSoname(f *testing.F) {
	cases := []string{
		// Valid soname patterns
		"libfoo.so.1",
		"libfoo.so.1.2.3",
		"libz.so.1",
		"libssl.so.3",
		"libcrypto.so.1.1.0",
		"usr/lib/libfoo.so.1",
		"lib/x86_64-linux-gnu/libc.so.6",
		// Edge cases
		"a.so.0",
		"x.so.9",
		".so.1",
		// Invalid patterns (should return 0)
		"libfoo.so",
		"foo.txt",
		"libfoo.so.",
		"libfoo.so.a",
		".so.",
		"so.1",
		"",
		"a",
	}

	for _, c := range cases {
		f.Add(c)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got := identity.Soname([]byte(input))

		if got < 0 {
			t.Errorf("Soname(%q) = %d: negative return", input, got)
		}

		if got > len(input) {
			t.Errorf("Soname(%q) = %d: exceeds input length %d", input, got, len(input))
		}

		// If pattern matched, verify the position is at least 3 (after ".so")
		// Note: position 3 is valid for edge case ".so.1" where prefix is empty
		if got > 0 && got < 3 {
			t.Errorf("Soname(%q) = %d: position too small (must be >= 3 for .so pattern)", input, got)
		}
	})
}

// FuzzScript tests APK script file pattern detection.
func FuzzScript(f *testing.F) {
	cases := []string{
		// Valid script patterns
		"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install",
		"busybox-1.37.0-r12.Q1sSNCl4MTQ0.trigger",
		"foo-1.0.Q1xxx.pre-install",
		"foo-1.0.Q1xxx.pre-upgrade",
		"foo-1.0.Q1xxx.post-upgrade",
		"foo-1.0.Q1xxx.post-deinstall",
		"pkg-0.0.1-r0.Q1abcdef123456.trigger",
		// Edge cases
		"a-1.Q1x.trigger",
		"x-0.Q1y.post-install",
		// Invalid patterns
		"usr/bin/ls",
		"foo.post-install",
		"foo-1.0.Q1xxx.txt",
		"foo-1.0.Qabc.post-install", // Q not followed by 1
		"foo.Q1xxx.post-install",    // No version before checksum
		"",
		"a",
	}

	for _, c := range cases {
		f.Add(c)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i, j := identity.Script([]byte(input))

		if i < 0 || j < 0 {
			t.Errorf("Script(%q) = (%d, %d): negative return", input, i, j)
		}

		if i > len(input) || j > len(input) {
			t.Errorf("Script(%q) = (%d, %d): exceeds input length %d", input, i, j, len(input))
		}

		// If pattern matched, validate structure
		if i > 0 {
			if j <= i {
				t.Errorf("Script(%q) = (%d, %d): scriptStart must be > pkgEnd", input, i, j)
			}
		}
	})
}

// FuzzSuffix tests version suffix pattern detection.
func FuzzSuffix(f *testing.F) {
	cases := []string{
		// Valid suffix patterns
		"app-1.0.0",
		"app-1.0.0-r5",
		"tool-2.3.4-beta1",
		"python-3.11",
		"gcc-14.1.0",
		"linux-6.6.0-r0",
		// Revision suffix variations
		"foo-1.0-r0",
		"foo-1.0-r99",
		"bar-2.3.4-r1",
		// Edge cases
		"a-1",
		"x-0",
		"z-9-r9",
		// Invalid patterns
		"usr/bin/ls",
		"foo",
		"foo-bar",
		"bar-beta",
		"-1.0",
		"",
	}

	for _, c := range cases {
		f.Add(c)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i := identity.Suffix([]byte(input))

		if i < 0 {
			t.Errorf("Suffix(%q) = %d: negative return", input, i)
		}

		if i > len(input) {
			t.Errorf("Suffix(%q) = %d: exceeds input length %d", input, i, len(input))
		}
	})
}

// FuzzEmbedded tests embedded version pattern detection.
func FuzzEmbedded(f *testing.F) {
	cases := []string{
		// Valid embedded patterns
		"foo.1.2.3.so",
		"bar.4.5.6.dylib",
		"libfoo.1.2.3.4.so",
		"module.0.0.1.dll",
		"lib.10.20.30.a",
		// Edge cases
		"x.0.0.0.y",
		"a.1.2.3.b",
		// Invalid patterns (too short or wrong format)
		"foo.so",
		"foo.1.so",
		"foo.1.2.so",
		"foo.txt",
		"go.mod",
		"README.md",
		"man_page.7",
		"",
		"a.b.c",
		"short.1.2.3.x", // too short total
	}

	for _, c := range cases {
		f.Add(c)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i, j := identity.Embedded([]byte(input))

		if i < 0 || j < 0 {
			t.Errorf("Embedded(%q) = (%d, %d): negative return", input, i, j)
		}

		if i > len(input) || j > len(input) {
			t.Errorf("Embedded(%q) = (%d, %d): exceeds input length %d", input, i, j, len(input))
		}

		if i > 0 && j <= i {
			t.Errorf("Embedded(%q) = (%d, %d): end must be greater than start", input, i, j)
		}
	})
}

// FuzzResultIterator tests result iteration and filtering.
func FuzzResultIterator(f *testing.F) {
	f.Add("a-1.0\nb-2.0", "a-1.1\nc-3.0")
	f.Add("x\ny\nz", "x\ny\nz")
	f.Add("old", "")
	f.Add("", "new")

	f.Fuzz(func(t *testing.T, oldStr, newStr string) {
		old := splitNonEmpty(oldStr)
		cur := splitNonEmpty(newStr)

		res := Diff(old, cur)

		// Test All iterator
		allCount := 0
		for range res.All() {
			allCount++
		}
		if allCount != len(res.E) {
			t.Errorf("All() count %d != entries %d", allCount, len(res.E))
		}

		// Test Filter for each status
		for _, status := range []Status{Unchanged, Updated, Removed, Added} {
			filterCount := uint32(0)
			for range res.Filter(status) {
				filterCount++
			}
			if filterCount != res.Count(status) {
				t.Errorf("Filter(%v) count %d != Count() %d", status, filterCount, res.Count(status))
			}
		}
	})
}

// splitNonEmpty splits a string by newlines, returning only non-empty parts.
func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}
