# Reconcile
This package is primarily intended to support diffing two separate string slices containing any number of file names (or individual files).

As opposed to calculating edit distance, this approach runs in O(n+m) time and handles lookups via O(1) maps. This allows for upwards of millions of files to be compared without the traditional (usually quadratic) comparison overhead encountered with edit distance algorithms. Instead of determining a normalized [0..1] score to detect "moves" or "changes", the reconciliation only determines whether a file was "updated" (unchanged files, removals, and additions are also supported).

Files are matched by their names in a way that supports semantic versioning and without needing something like a greedy algorithm to store the highest normalized score for each pair of files (e.g., the Hungarian Algorithm).

For example, the following file pairs share an identity:
- `libfoo.so.1.0.0` and `libfoo.so.2.0.0` (`libfoo.so`)
- `app-1.0.0-r0` and `app-2.0.0-r1` (`app`)
- `foo.1.2.3.so` and `foo.3.4.5.so` (`foo` + `.so`)
- `file-1.txt` and `file-2.txt` (`file` + `.txt`)

Files without a version pattern are compared directly (e.g., `foo` and `bar`, which do not share an identity).

Files of the same name with different extensions do not share an identity: `file-1.txt` and `file-2.pdf` are a removal plus an addition, not an update, and `libfoo.so.1.conf` never shares an identity with the library `libfoo.so.1`.

Identical names always reconcile as `Unchanged`, even when other files share their identity: exact matches are claimed in a dedicated pass before any identity matching happens, and every name that shares a key remains matchable (a soname symlink and its target both update across a version bump).

`Diff` and `DiffWith` are safe to call concurrently. Large inputs are hashed, indexed, and reconciled in parallel; small inputs run sequentially to avoid the coordination overhead. Results are deterministic: byte-for-byte identical for any worker count, GOMAXPROCS value, or goroutine schedule.

Each input list must contain fewer than 2^32-1 entries; larger inputs panic rather than silently truncating indices.

## Usage

```sh
go get github.com/egibs/reconcile@latest
```

```go
import "github.com/egibs/reconcile/pkg/files"

srcPaths := []string{"foo.txt", "bar.txt"}
destPaths := []string{"baz.txt", "another_file"}

result := files.Diff(srcPaths, destPaths)
```

Custom identity schemes plug in through `DiffWith` options:

```go
// Custom hash and identity-equality functions.
result := files.DiffWith(old, cur, files.WithIdentity(hashFn, equalFn))

// Normalized exact matching (e.g., case-insensitive names).
result := files.DiffWith(old, cur,
    files.WithIdentity(foldedHashFn, strings.EqualFold),
    files.WithExactEquality(strings.EqualFold))
```

## Stages

There are five stages involved in determining a final result containing the files which are `Unchanged`, `Updated`, `Removed`, or `Added`. On multi-core hosts with large inputs, every stage runs in parallel.

1. Identity and exact hashes for all files are calculated.
1. Files are grouped into bucket maps keyed by identity and exact hash to enable O(1) lookups; every index sharing a key stays matchable.
1. The exact pass claims byte-identical pairs as `Unchanged`.
1. The identity pass claims remaining same-identity pairs as `Updated`; unmatched old files are `Removed`.
1. Unmatched new files are marked `Added` and the entries are merged into a final result type.

## SIMD (experimental)

`internal/identity` carries `simd/archsimd` (Go 1.27, `GOEXPERIMENT=simd`) variants of its
byte-scan kernels for amd64 (AVX) and arm64 (Neon), plus portable `simd` fallbacks. The files
are build-tagged on `goexperiment.simd`, so normal builds are unaffected; `make test-simd`
cross-checks every kernel against its scalar counterpart and `make bench-simd` compares them.

Findings so far (Apple M3 Pro, arm64; raw numbers under [SIMD kernels](#simd-kernels)):

- Vector classification wins on names whose version tails or dot-free suffixes span 16+ bytes
  (up to 2x on `Soname`, 1.7x on `Embedded`), and loses slightly on short names where the
  fixed mask-extraction cost exceeds the few scalar iterations it replaces.
- The `Suffix` version-segment walk is a sequential state machine and does not vectorize.
- The flag-application ops (`&^`/`|` over hash slices) are memory-bound; scalar, archsimd,
  and portable simd all run at identical throughput.
- The additions scan was the largest opportunity, and it did not need SIMD: `Diff` now scans
  the claim bitset a word at a time (`word-atomic` below), beating the previous per-bit
  atomic loads by 2x (10% claimed) to 10x (90% claimed) on that stage; a 128-bit archsimd
  skip would add ~30% more only in claim-heavy cases.
- The portable `simd` package exposes no mask-to-bitmask conversion, so position extraction
  spills lanes through memory and generally runs at or below scalar speed for these scans.

## Benchmarks

```
BenchmarkHash
BenchmarkHash-12              	11079291	       107.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-12            	   10000	    106120 ns/op	  236378 B/op	      64 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-12           	     878	   1345020 ns/op	 3408521 B/op	     626 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-12          	      78	  13609124 ns/op	31172647 B/op	    1772 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-12            	       6	 178736062 ns/op	462201840 B/op	   17132 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-12           	       1	2159298666 ns/op	3815294120 B/op	  131822 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-12         	       3	 337566583 ns/op	260652440 B/op	    8240 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-12         	       3	 344974889 ns/op	260659408 B/op	    8265 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-12         	       4	 257016729 ns/op	462159344 B/op	   16609 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-12         	       6	 204885257 ns/op	462157885 B/op	   16805 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-12        	       6	 190847639 ns/op	462171544 B/op	   17201 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-12                   	       6	 188422514 ns/op	       462.2 MB-alloc/op	462201613 B/op	   17130 allocs/op
BenchmarkSoname
BenchmarkSoname-12                     	69499412	        17.52 ns/op	       0 B/op	       0 allocs/op
BenchmarkScript
BenchmarkScript-12                     	16552617	        70.76 ns/op	       0 B/op	       0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-12                     	15586228	        79.45 ns/op	       0 B/op	       0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-12                   	45074935	        28.27 ns/op	       0 B/op	       0 allocs/op
BenchmarkSpans
BenchmarkSpans-12                      	13420404	        91.22 ns/op	       0 B/op	       0 allocs/op
BenchmarkEqual
BenchmarkEqual-12                      	 7014228	       170.4 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	github.com/egibs/reconcile/pkg/files	27.163s
```

### SIMD kernels

`make bench-simd` results comparing the scalar kernels against their `simd/archsimd` and
portable `simd` variants (`GOEXPERIMENT=simd`). The `short` corpora are typical package
paths; the `long` corpora have version tails or dot-free suffixes spanning 16+ bytes.

```
goos: darwin
goarch: arm64
pkg: github.com/egibs/reconcile/internal/identity
cpu: Apple M3 Pro
BenchmarkSIMDSoname/soname/short/scalar-12         	61275747	        19.16 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSoname/soname/short/archsimd-12       	78407030	        15.13 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSoname/soname/short/portable-12       	51064915	        23.84 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSoname/soname/long/scalar-12          	36963642	        31.59 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSoname/soname/long/archsimd-12        	64179133	        17.37 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSoname/soname/long/portable-12        	36968244	        31.61 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDEmbedded/embedded/short/scalar-12     	52643794	        22.12 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDEmbedded/embedded/short/archsimd-12   	46898586	        26.29 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDEmbedded/embedded/long/scalar-12      	17107226	        70.56 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDEmbedded/embedded/long/archsimd-12    	30598747	        40.27 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSuffix/suffix/short/scalar-12         	14726794	        86.79 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSuffix/suffix/short/archsimd-12       	13921738	        87.31 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSuffix/suffix/long/scalar-12          	 7341150	       170.8 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSuffix/suffix/long/archsimd-12        	 7993933	       155.2 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSpans/spans/short/scalar-12           	34255495	        35.68 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSpans/spans/short/archsimd-12         	33750211	        35.12 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSpans/spans/long/scalar-12            	 9939681	       121.3 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDSpans/spans/long/archsimd-12          	15226791	        78.95 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/scalar-12       	99486681	        12.50 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/archsimd-12     	77010700	        15.35 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/portable-12     	55138083	        20.98 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/scalar-12        	48313879	        25.90 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/archsimd-12      	72583939	        16.16 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/portable-12      	31009287	        37.59 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDApplyFlags/scalar-12                  	  540820	      2275 ns/op	28811.59 MB/s	       0 B/op	       0 allocs/op
BenchmarkSIMDApplyFlags/archsimd-12                	  468980	      2490 ns/op	26319.37 MB/s	       0 B/op	       0 allocs/op
BenchmarkSIMDApplyFlags/portable-12                	  482306	      2484 ns/op	26383.40 MB/s	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/perbit-atomic-12         	     856	   1432404 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/word-atomic-12           	    1879	    641020 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/word-scalar-12           	    1771	    682306 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/archsimd-12              	    1810	    665743 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/perbit-atomic-12         	    1087	   1078996 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/word-atomic-12           	   10000	    109124 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/word-scalar-12           	   13555	     86646 ns/op	       0 B/op	       0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/archsimd-12              	   15385	     76534 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	github.com/egibs/reconcile/internal/identity	42.350s
```
