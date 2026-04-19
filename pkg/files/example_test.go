package files_test

import (
	"fmt"

	"github.com/egibs/reconcile/pkg/files"
)

func ExampleDiff() {
	old := []string{"libfoo.so.1.0.0", "libbar.so.2.0.0", "removed.txt"}
	cur := []string{"libfoo.so.2.0.0", "libbar.so.2.0.0", "added.txt"}
	r := files.Diff(old, cur)
	fmt.Printf("unchanged=%d updated=%d removed=%d added=%d\n",
		r.Count(files.Unchanged), r.Count(files.Updated),
		r.Count(files.Removed), r.Count(files.Added))
	// Output:
	// unchanged=1 updated=1 removed=1 added=1
}

func ExampleResult_Filter() {
	old := []string{"lib.so.1", "app-1.0.0", "keep.txt"}
	cur := []string{"lib.so.2", "app-2.0.0", "keep.txt"}
	r := files.Diff(old, cur)
	var count int
	for range r.Filter(files.Updated) {
		count++
	}
	fmt.Println(count)
	// Output:
	// 2
}

func ExampleResult_All() {
	old := []string{"foo-1.0", "bar-1.0"}
	cur := []string{"foo-2.0", "bar-2.0"}
	r := files.Diff(old, cur)
	var n int
	for range r.All() {
		n++
	}
	fmt.Println(n)
	// Output:
	// 2
}
