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

## Benchmarks

```
goos: linux
goarch: amd64
pkg: github.com/egibs/reconcile/pkg/files
cpu: Intel(R) Core(TM) i9-14900K
BenchmarkHash
BenchmarkHash-32                14828508                72.18 ns/op            0 B/op          0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-32                  9660            149458 ns/op          236381 B/op         64 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-32                  458           2540887 ns/op         3408629 B/op        626 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-32                  78          15366147 ns/op        31183166 B/op       2641 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-32                     8         129905718 ns/op        462331910 B/op     18004 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-32                    1        1085729599 ns/op        3815390648 B/op   132687 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-32                 4         270394520 ns/op        260652440 B/op      8240 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-32                 4         265733210 ns/op        260659464 B/op      8265 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-32                 4         298481384 ns/op        462159400 B/op     16610 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-32                 5         214345990 ns/op        462158116 B/op     16807 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-32                7         150816161 ns/op        462171852 B/op     17204 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-32                           9         120140791 ns/op               462.3 MB-alloc/op        462330873 B/op     17995 allocs/op
BenchmarkSoname
BenchmarkSoname-32                      114521086               10.48 ns/op            0 B/op          0 allocs/op
BenchmarkScript
BenchmarkScript-32                      16052916                73.51 ns/op            0 B/op          0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-32                      22602453                54.67 ns/op            0 B/op          0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-32                    40496424                25.20 ns/op            0 B/op          0 allocs/op
BenchmarkSpans
BenchmarkSpans-32                       14926018                71.73 ns/op            0 B/op          0 allocs/op
BenchmarkEqual
BenchmarkEqual-32                        8474408               144.4 ns/op             0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/pkg/files    27.420s
```
