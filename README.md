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
import "github.com/egibs/reconcile/pkg/files"

srcPaths := []string{"foo.txt", "bar.txt"}
destPaths := []string{"baz.txt", "another_file"}

result := files.Diff(srcPaths, destPaths)
```

## Stages

There are five [concurrent] stages involved in determining a final result containing the files which are `Unchanged`, `Updated`, `Removed`, or `Added`.

1. Identities and hashes for all files are calculated in parallel.
1. A map of new files is constructed to enable O(1) lookups.
1. Old files and new files are compared with matches being marked (and are otherwise treated as removals).
1. Unmatched files are marked as additions.
1. Results are merged into a final result type.

## Benchmarks

```
goos: darwin
goarch: arm64
pkg: github.com/egibs/reconcile/pkg/files
cpu: Apple M4 Max
BenchmarkHash
BenchmarkHash-16                19662140                60.06 ns/op            0 B/op          0 allocs/op
BenchmarkDiff1K
BenchmarkDiff1K-16                  9457            122674 ns/op          243809 B/op       1182 allocs/op
BenchmarkDiff10K
BenchmarkDiff10K-16                 1616            752013 ns/op         1233231 B/op       1182 allocs/op
BenchmarkDiff100K
BenchmarkDiff100K-16                 178           6672506 ns/op        10693006 B/op       1183 allocs/op
BenchmarkDiff1M
BenchmarkDiff1M-16                    12          95580521 ns/op        134849744 B/op      8864 allocs/op
BenchmarkDiff10M
BenchmarkDiff10M-16                    1        1209949958 ns/op        1196639216 B/op    66206 allocs/op
BenchmarkDiff1M_Workers
BenchmarkDiff1M_Workers/w=1
BenchmarkDiff1M_Workers/w=1-16                 6         175548583 ns/op        134833512 B/op      8726 allocs/op
BenchmarkDiff1M_Workers/w=2
BenchmarkDiff1M_Workers/w=2-16                 8         125657932 ns/op        134842238 B/op      8735 allocs/op
BenchmarkDiff1M_Workers/w=4
BenchmarkDiff1M_Workers/w=4-16                10         100486429 ns/op        134843275 B/op      8753 allocs/op
BenchmarkDiff1M_Workers/w=8
BenchmarkDiff1M_Workers/w=8-16                12          94395385 ns/op        134845381 B/op      8789 allocs/op
BenchmarkDiff1M_Workers/w=16
BenchmarkDiff1M_Workers/w=16-16               12          92816580 ns/op        134849550 B/op      8861 allocs/op
BenchmarkMemory1M
BenchmarkMemory1M-16                    1000000000               0.1986 ns/op          134.8 MB-alloc          134.8 MB-heap           0 B/op          0 allocs/op
BenchmarkSoname
BenchmarkSoname-16                      153957822                7.898 ns/op           0 B/op          0 allocs/op
BenchmarkScript
BenchmarkScript-16                      19530720                61.43 ns/op            0 B/op          0 allocs/op
BenchmarkSuffix
BenchmarkSuffix-16                      58501389                19.46 ns/op            0 B/op          0 allocs/op
BenchmarkEmbedded
BenchmarkEmbedded-16                    63251659                19.78 ns/op            0 B/op          0 allocs/op
BenchmarkSpans
BenchmarkSpans-16                       18371892                64.42 ns/op            0 B/op          0 allocs/op
BenchmarkEqual
BenchmarkEqual-16                        9969672               121.9 ns/op             0 B/op          0 allocs/op
PASS
ok      github.com/egibs/reconcile/pkg/files    25.122s
```
