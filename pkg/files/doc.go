// Package files provides concurrent O(n+m) reconciliation of two string slices.
// Diff compares old and new lists and classifies each entry as Unchanged, Updated,
// Removed, or Added using identity matching. DiffWith accepts custom identity
// functions via Option values for use with non-file-path string collections.
package files
