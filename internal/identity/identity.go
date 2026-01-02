package identity

import (
	"bytes"
	"unsafe"
)

// Equal checks if two strings have the same identity.
// This is used to verify identity matches after hash lookup while handleing collisions.
//
// Two strings have the same identity if their identity spans are equal.
// The identity span is the portion of the filename excluding version numbers.
func Equal(old, cur string) bool {
	obs := unsafe.Slice(unsafe.StringData(old), len(old))
	cbs := unsafe.Slice(unsafe.StringData(cur), len(cur))

	oj, os, oe := Spans(obs)
	cj, cs, ce := Spans(cbs)

	// Return early if the identities are different (unequal or different lengths).
	if oj != cj || oe-os != ce-cs {
		return false
	}

	return bytes.Equal(obs[:oj], cbs[:cj]) && bytes.Equal(obs[os:oe], cbs[cs:ce])
}

// Spans returns the byte ranges that comprise the identity of a filename.
// Returns (j, s, e) where [0:j] is the first span and [s:e] is the second span.
// For most patterns, only the first span is used (s == e == 0).
// For embedded versions and scripts, both spans are used (prefix [0:j] and suffix [s:len]).
func Spans(bs []byte) (j, s, e int) {
	length := len(bs)

	if r := Soname(bs); r > 0 {
		return r, 0, 0
	}

	if r1, r2 := Script(bs); r1 > 0 {
		return r1, r2, length
	}

	if r1, r2 := Embedded(bs); r1 > 0 {
		return r1, r2, length
	}

	if r1 := Suffix(bs); r1 > 0 {
		return r1, 0, 0
	}

	return length, 0, 0
}

// Soname detects shared library versioning pattern: name.so.VERSION
// Returns the position of the version separator (after ".so"), or 0 if not found.
func Soname(bs []byte) int {
	length := len(bs)

	// Scan backwards looking for ".so.N" pattern,
	// returning the position just after ".so".
	for i := length - 2; i >= 3; i-- {
		if bs[i] == '.' && bs[i+1]-'0' < 10 && bs[i-1] == 'o' && bs[i-2] == 's' && bs[i-3] == '.' {
			return i
		}
	}

	return 0
}

// Embedded detects embedded version pattern: name.VERSION.ext
// Returns (start, end) of the version portion, or (0, 0) if not found.
func Embedded(bs []byte) (int, int) {
	length := len(bs)
	if length < 9 {
		return 0, 0
	}

	// Find the last extension separator (usually `.`).
	ext := -1
	for i := length - 1; i > 0; i-- {
		if bs[i] == '.' {
			ext = i
			break
		}
	}

	if ext < 6 || ext == length-1 {
		return 0, 0
	}

	// Scan backwards from the extension looking for any version pattern.
	i, dots := ext-1, 0
	for i >= 0 && (bs[i]-'0' < 10 || bs[i] == '.') {
		if bs[i] == '.' {
			dots++
		}
		i--
	}

	// Require at least 2 dots in version (e.g., "1.2.3").
	if dots >= 2 && i >= 0 && bs[i+1] == '.' && bs[i+2]-'0' < 10 {
		return i + 1, ext
	}

	return 0, 0
}

// Script detects script file patterns with checksums.
// Examples:
// "alpine-baselayout-3.6.8-r1.Q17OteNVXn9/iSXcJI1Vf8x0TVc9Y=.post-install"
// "busybox-1.37.0-r12.Q1sSNCl4MTQ0d1V/0NTXAhIjY7Nqo=.trigger"
//
// Returns (pkgEnd, scriptStart) where identity = name[:pkgEnd] + name[scriptStart:].
// or (0, 0) if the pattern not detected.
func Script(bs []byte) (int, int) {
	length := len(bs)
	if length < 20 {
		return 0, 0
	}

	suffixes := []string{
		".post-deinstall",
		".post-install",
		".post-upgrade",
		".pre-install",
		".pre-upgrade",
		".trigger",
	}

	start := 0

	for _, suffix := range suffixes {
		if length > len(suffix) && string(bs[length-len(suffix):]) == suffix {
			start = length - len(suffix)
			break
		}
	}

	if start == 0 {
		return 0, 0
	}

	checksumStart := -1

	for i := start - 2; i >= 4; i-- {
		if bs[i] == '.' && i+2 < length && bs[i+1] == 'Q' && bs[i+2] == '1' {
			checksumStart = i
			break
		}
	}

	if checksumStart < 0 {
		return 0, 0
	}

	for i := checksumStart - 1; i >= 1; i-- {
		if bs[i] == '-' && i+1 < checksumStart && bs[i+1]-'0' < 10 {
			return i, start
		}
	}

	return 0, 0
}

// Suffix detects version suffix pattern: name-VERSION or name-VERSION-rN.
func Suffix(bs []byte) int {
	length := len(bs)
	i := length - 1

	// Handle optional "-rN" revision suffixes (APK convention).
	if i > 2 && bs[i]-'0' < 10 {
		for i >= 0 && bs[i]-'0' < 10 {
			i--
		}

		if i > 0 && bs[i] == 'r' && bs[i-1] == '-' {
			i -= 2
		}
	}

	// Scan backwards looking for "-N" pattern where N is a digit.
	for i >= 0 {
		c := bs[i]
		if c == '-' && i+1 < length && bs[i+1]-'0' < 10 {
			return i
		}

		// Continue scanning through valid version characters.
		if c-'0' < 10 || c == '.' || c == '-' || c == '+' || (c|32)-'a' < 26 {
			i--
			continue
		}

		break
	}

	return 0
}
