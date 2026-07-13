package identity

import (
	"bytes"
	"unsafe"
)

// Equal checks if two strings have the same identity.
// This is used to verify identity matches after hash lookup while handling collisions.
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
// For patterns without a preserved suffix, only the first span is used (s == e == 0).
// For embedded versions, scripts, and suffix patterns with a file extension,
// both spans are used (prefix [0:j] and suffix [s:e]).
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

	if r1, r2 := Suffix(bs); r1 > 0 {
		if r2 == length {
			return r1, 0, 0
		}
		return r1, r2, length
	}

	return length, 0, 0
}

// Soname detects the shared library versioning pattern: name.so.VERSION,
// where VERSION is a trailing run of digits and dots (e.g., "1", "1.2.3").
// The version must terminate the name: "libfoo.so.1.conf" is not a soname,
// so files with a different extension never share a library's identity.
// Returns the position of the version separator (after ".so"), or 0 if not found.
func Soname(bs []byte) int {
	length := len(bs)

	// The name must end with a digit for a trailing version to exist.
	i := length - 1
	if i < 4 || bs[i]-'0' >= 10 {
		return 0
	}

	// Walk backwards over the version tail (digits and dots).
	for i >= 0 && (bs[i]-'0' < 10 || bs[i] == '.') {
		i--
	}

	// The tail must be preceded by ".so" and start with its own separator dot.
	if i >= 2 && bs[i] == 'o' && bs[i-1] == 's' && bs[i-2] == '.' && bs[i+1] == '.' {
		return i + 1
	}

	return 0
}

// Embedded detects embedded version pattern: name.VERSION.ext
// The version must have at least three numeric components (e.g., "1.2.3");
// two-component patterns like "foo.1.2.so" are left to other matchers so that
// date- or chapter-style names ("report.2024.12.pdf") are not version-stripped.
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

	// Require at least 3 dots counting the separator preceding the version,
	// i.e., versions with 3+ numeric components such as "1.2.3".
	if dots >= 3 && i >= 0 && bs[i+1] == '.' && bs[i+2]-'0' < 10 {
		return i + 1, ext
	}

	return 0, 0
}

// scriptSuffixes are the APK install-script suffixes recognized by Script.
var scriptSuffixes = [...]string{
	".post-deinstall",
	".post-install",
	".post-upgrade",
	".pre-install",
	".pre-upgrade",
	".trigger",
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

	start := 0

	for _, suffix := range &scriptSuffixes {
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

// Suffix detects the trailing version pattern: name-VERSION[.ext]
//
// VERSION consists of one or more '-'-separated segments, each of which must
// contain at least one digit; the first segment must begin with a digit.
// This matches "app-1.0.0", "app-1.0.0-r5", and "tool-2.3.4-beta1" while
// rejecting hyphenated sibling names like "openssl-3-doc" or "gtk-3-demo",
// whose trailing segments carry no digits.
//
// A trailing ".ext" segment beginning with a letter is treated as the file
// extension and remains part of the identity, so "file-1.txt" and
// "file-2.pdf" do not share an identity.
//
// Returns (versionStart, extStart) where identity = name[:versionStart] +
// name[extStart:], with extStart == len(bs) when there is no extension.
// Returns (0, 0) if the pattern is not detected.
func Suffix(bs []byte) (int, int) {
	length := len(bs)

	// Split off a trailing extension: a final '.'-segment starting with a letter.
	end := length
	if dot := bytes.LastIndexByte(bs, '.'); dot > 0 && dot+1 < length && isAlpha(bs[dot+1]) {
		end = dot
	}

	verStart := 0
	segHasDigit := false
	segLen := 0

	for i := end - 1; i >= 0; i-- {
		c := bs[i]

		if c == '-' {
			// A version segment must be non-empty and contain a digit.
			if segLen == 0 || !segHasDigit {
				break
			}
			// The first (leftmost) version segment must begin with a digit.
			if bs[i+1]-'0' < 10 {
				verStart = i
			}
			segHasDigit, segLen = false, 0
			continue
		}

		// Version segments may contain digits, dots, '+', and letters
		// (pre-release tags like "beta1"); anything else ends the scan.
		if c-'0' < 10 {
			segHasDigit = true
		} else if c != '.' && c != '+' && !isAlpha(c) {
			break
		}
		segLen++
	}

	if verStart == 0 {
		return 0, 0
	}

	return verStart, end
}

// isAlpha reports whether c is an ASCII letter.
func isAlpha(c byte) bool {
	return (c|32)-'a' < 26
}
