# Reconcile
This package is primarily intended to support diffing two separate string slices containing any number of file names (or individual files).

As opposed to calculating edit distance, this approach runs in O(n+m) time and handles lookups via O(1) maps. This allows for upwards of millions of files to be compared without the traditional (usually quadratic) comparison overhead encountered with edit distance algorithms. Instead of determining a normalized [0..1] score to detect "moves" or "changes", the reconciliation only determines whether a file was "updated" (unchanged files, removals, and additions are also supported).

Files are matched by their names in a way that supports semantic versioning and without needing something like a greedy algorithm to store the highest normalized score for each pair of files (e.g., the Hungarian Algorithm).

For example, the following file pairs share an identity:
- `libfoo.so.1.0.0` and `libfoo.so.2.0.0` (`libfoo`)
- `app-1.0.0-r0` and `app-2.0.0-r1` (`app`)
- `foo.1.2.3.so` and `foo.3.4.5.so` (`foo`)

Files without an extension are compared directly (e.g., `foo` and `bar` which do not share an identity).

Files of the same name with different extensions do not share an identity.

`Diff`/`diffP` are safe to call concurrently since each shard (Goroutine) maintains its own internal state.

## Usage

```sh
go get github.com/egibs/reconcile@latest
```

```go
import "github.com/egibs/reconcile"

srcPaths := []string{"foo.txt", "bar.txt"}
destPaths := []string{"baz.txt", "another_file"}

result := reconcile.Diff(srcPaths, destPaths)
```

## Stages

There are five [concurrent] stages involved in determining a final result containing the files which are `Unchanged`, `Updated`, `Removed`, or `Added`.

1. Identities and hashes for all files are calculated in parallel.
1. A map of new files is constructed to enable O(1) lookups.
1. Old files and new files are compared with matches being marked (and are otherwise treated as removals).
1. Unmatched files are marked as additions.
1. Results are merged into a final result type.

## Benchmarks

Linux (amd64):
```
goos: linux
goarch: amd64
pkg: github.com/egibs/reconcile/pkg/diff
cpu: Intel(R) Core(TM) i9-14900K
BenchmarkHash
BenchmarkHash-32                16910768                61.85 ns/op            0 B/op          0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-32                  6142            774729 ns/op          261289 B/op       1422 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-32                 1806           1147560 ns/op         1259210 B/op       1424 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-32                 153           8541098 ns/op        10815391 B/op       1425 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-32                    34          45702186 ns/op        135056045 B/op      9105 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-32                    3         333732898 ns/op        1196846048 B/op    66452 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-32                 6         188541735 ns/op        134833800 B/op      8730 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-32                 9         116068515 ns/op        134850992 B/op      8742 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-32                14          73552229 ns/op        134869012 B/op      8766 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-32                21          53134110 ns/op        134905156 B/op      8815 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-32               24          61156419 ns/op        134911688 B/op      8912 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-32                    1000000000               0.2004 ns/op          135.1 MB-alloc          135.0 MB-heap           0 B/op          0 allocs/op
BenchmarkSoname
BenchmarkSoname-32                      91654180                13.08 ns/op            0 B/op          0 allocs/op
BenchmarkApkscript
BenchmarkApkscript-32                   18917796                63.29 ns/op            0 B/op          0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-32                      58387150                20.75 ns/op            0 B/op          0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-32                    60699500                19.00 ns/op            0 B/op          0 allocs/op
BenchmarkSpans
BenchmarkSpans-32                       16002042                75.49 ns/op            0 B/op          0 allocs/op
BenchmarkIdEq
BenchmarkIdEq-32                         7815445               152.8 ns/op             0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/pkg/diff     31.836s
```

macOS (arm64):
```
goos: darwin
goarch: arm64
pkg: github.com/egibs/reconcile/pkg/diff
cpu: Apple M4 Max
BenchmarkHash
BenchmarkHash-16                20238174                58.12 ns/op            0 B/op          0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-16                  8952            119613 ns/op          247406 B/op       1234 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-16                 1850            649774 ns/op         1254903 B/op       1238 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-16                 280           3676539 ns/op        10803199 B/op       1235 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-16                    28          37522251 ns/op        134910648 B/op      8915 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-16                    3         425966653 ns/op        1196700416 B/op    66260 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-16                 6         172579271 ns/op        134833832 B/op      8734 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-16                12          98631667 ns/op        134851000 B/op      8746 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-16                18          62911887 ns/op        134868869 B/op      8770 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-16                27          41234681 ns/op        134904643 B/op      8818 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-16               31          37688981 ns/op        134910624 B/op      8915 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-16                    1000000000               0.1454 ns/op          134.9 MB-alloc          134.9 MB-heap           0 B/op          0 allocs/op
BenchmarkSoname
BenchmarkSoname-16                      100000000               10.69 ns/op            0 B/op          0 allocs/op
BenchmarkApkscript
BenchmarkApkscript-16                   17632774                65.56 ns/op            0 B/op          0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-16                      61511253                18.83 ns/op            0 B/op          0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-16                    60355089                19.90 ns/op            0 B/op          0 allocs/op
BenchmarkSpans
BenchmarkSpans-16                       17876661                65.17 ns/op            0 B/op          0 allocs/op
BenchmarkIdEq
BenchmarkIdEq-16                        10234213               118.4 ns/op             0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/pkg/diff     26.870s
?       github.com/egibs/reconcile/pkg/hash     [no test files]
?       github.com/egibs/reconcile/pkg/identity [no test files]
```
