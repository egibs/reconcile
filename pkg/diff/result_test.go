package diff

import "testing"

func TestResult_Iterators(t *testing.T) {
	old := []string{"a.so.1", "b.so.1", "old.txt"}
	cur := []string{"a.so.1", "b.so.2", "new.txt"}

	r := Diff(old, cur)

	var count int
	for range r.All() {
		count++
	}
	if count != len(r.E) {
		t.Errorf("All() yielded %d, want %d", count, len(r.E))
	}

	var updated int
	for range r.Filter(Updated) {
		updated++
	}

	if uint32(updated) != r.Count(Updated) {
		t.Errorf("Filter(Updated) yielded %d, want %d", updated, r.Count(Updated))
	}
}
