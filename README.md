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
cpu: AMD Ryzen 7 7840U w/ Radeon  780M Graphics     
BenchmarkHash-16              	14007163	        86.65 ns/op	       0 B/op	       0 allocs/op
BenchmarkDiff1K-16            	    8116	    143867 ns/op	  236378 B/op	      64 allocs/op
BenchmarkDiff10K-16           	     553	   2406102 ns/op	 3408597 B/op	     626 allocs/op
BenchmarkDiff100K-16          	      92	  13295660 ns/op	31153001 B/op	    1841 allocs/op
BenchmarkDiff1M-16            	       6	 171970322 ns/op	462171509 B/op	   17200 allocs/op
BenchmarkDiff10M-16           	       1	2893765994 ns/op	3815231432 B/op	  131894 allocs/op
BenchmarkDiff1M_Workers/w=1-16         	       3	 458755181 ns/op	260652477 B/op	    8240 allocs/op
BenchmarkDiff1M_Workers/w=2-16         	       3	 495926174 ns/op	260659408 B/op	    8265 allocs/op
BenchmarkDiff1M_Workers/w=4-16         	       3	 437167420 ns/op	462159512 B/op	   16611 allocs/op
BenchmarkDiff1M_Workers/w=8-16         	       4	 298978862 ns/op	462157904 B/op	   16805 allocs/op
BenchmarkDiff1M_Workers/w=16-16        	       6	 199819876 ns/op	462171581 B/op	   17201 allocs/op
BenchmarkMemory1M-16                   	       6	 179482950 ns/op	       462.2 MB-alloc/op	462171842 B/op	   17203 allocs/op
BenchmarkSoname-16                     	91837096	        13.72 ns/op	       0 B/op	       0 allocs/op
BenchmarkScript-16                     	13881259	        86.72 ns/op	       0 B/op	       0 allocs/op
BenchmarkSuffix-16                     	19892607	        61.89 ns/op	       0 B/op	       0 allocs/op
BenchmarkEmbedded-16                   	47366329	        25.66 ns/op	       0 B/op	       0 allocs/op
BenchmarkSpans-16                      	15214172	        75.36 ns/op	       0 B/op	       0 allocs/op
BenchmarkEqual-16                      	 7907661	       156.9 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	github.com/egibs/reconcile/pkg/files	33.501s
```
