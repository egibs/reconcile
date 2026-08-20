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
goos: darwin
goarch: arm64
pkg: github.com/egibs/reconcile/pkg/files
cpu: Apple M4 Max
BenchmarkHash
BenchmarkHash-16                16077561                74.59 ns/op            0 B/op          0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-16                 13363             92498 ns/op          236378 B/op         64 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-16                  919           1307270 ns/op         3408552 B/op        626 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-16                  79          12994233 ns/op        31155550 B/op       1841 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-16                     8         133556614 ns/op        462173166 B/op     17205 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-16                    1        1443152375 ns/op        3815231432 B/op   131894 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-16                 5         238474583 ns/op        260652440 B/op      8240 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-16                 5         229912208 ns/op        260659408 B/op      8265 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-16                 5         210769600 ns/op        462159288 B/op     16609 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-16                 6         173165604 ns/op        462157848 B/op     16805 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-16                8         134437979 ns/op        462171334 B/op     17199 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-16                           8         137126089 ns/op               462.2 MB-alloc/op        462171418 B/op     17199 allocs/op
BenchmarkSoname
BenchmarkSoname-16                      100000000               10.75 ns/op            0 B/op          0 allocs/op
BenchmarkScript
BenchmarkScript-16                      21566571                57.37 ns/op            0 B/op          0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-16                      20436314                58.54 ns/op            0 B/op          0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-16                    62497150                19.86 ns/op            0 B/op          0 allocs/op
BenchmarkSpans
BenchmarkSpans-16                       20144973                58.47 ns/op            0 B/op          0 allocs/op
BenchmarkEqual
BenchmarkEqual-16                       10752061               113.6 ns/op             0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/pkg/files    28.424s
```

### SIMD kernels

`make bench-simd` results comparing the scalar kernels against their `simd/archsimd` and
portable `simd` variants (`GOEXPERIMENT=simd`). The `short` corpora are typical package
paths; the `long` corpora have version tails or dot-free suffixes spanning 16+ bytes.

```
goos: darwin
goarch: arm64
pkg: github.com/egibs/reconcile/internal/identity
cpu: Apple M4 Max
BenchmarkSIMDSoname/soname/short/scalar-16              89212245                13.16 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSoname/soname/short/archsimd-16            100000000               11.92 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSoname/soname/short/portable-16            64966412                17.75 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSoname/soname/long/scalar-16               53653064                21.25 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSoname/soname/long/archsimd-16             90638431                12.79 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSoname/soname/long/portable-16             48349323                24.24 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDEmbedded/embedded/short/scalar-16          79201604                14.78 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDEmbedded/embedded/short/archsimd-16        62995432                17.75 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDEmbedded/embedded/long/scalar-16           25035684                48.29 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDEmbedded/embedded/long/archsimd-16         39375354                30.66 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSuffix/suffix/short/scalar-16              18952642                61.31 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSuffix/suffix/short/archsimd-16            19897651                58.83 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSuffix/suffix/long/scalar-16                9344050               127.0 ns/op             0 B/op          0 allocs/op
BenchmarkSIMDSuffix/suffix/long/archsimd-16             11497941               104.1 ns/op             0 B/op          0 allocs/op
BenchmarkSIMDSpans/spans/short/scalar-16                50430850                25.16 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSpans/spans/short/archsimd-16              48180272                24.18 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSpans/spans/long/scalar-16                 13365639                90.06 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDSpans/spans/long/archsimd-16               19618982                60.20 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/scalar-16            175173360                6.880 ns/op           0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/archsimd-16          100000000               11.23 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/short/portable-16          79528791                15.16 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/scalar-16             66146679                18.46 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/archsimd-16           100000000               11.27 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDLastDot/lastdot/long/portable-16           39370401                29.73 ns/op            0 B/op          0 allocs/op
BenchmarkSIMDApplyFlags/scalar-16                         643334              1718 ns/op        38155.28 MB/s          0 B/op          0 allocs/op
BenchmarkSIMDApplyFlags/archsimd-16                       669909              1739 ns/op        37689.94 MB/s          0 B/op          0 allocs/op
BenchmarkSIMDApplyFlags/portable-16                       603830              1874 ns/op        34963.21 MB/s          0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/perbit-atomic-16               1018           1170888 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/word-atomic-16                 2156            562929 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/word-scalar-16                 2118            568950 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=10%/archsimd-16                    2133            569958 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/perbit-atomic-16               1321            913543 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/word-atomic-16                10000            103951 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/word-scalar-16                16417             72122 ns/op               0 B/op          0 allocs/op
BenchmarkSIMDUnmarkedScan/marked=90%/archsimd-16                   19692             56927 ns/op               0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/internal/identity    41.278s
```
