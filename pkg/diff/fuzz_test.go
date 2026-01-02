package diff

import (
	"reflect"
	"testing"

	"github.com/egibs/reconcile/internal/identity"
)

func FuzzDiff(f *testing.F) {
	cases := []struct {
		a, b string
	}{
		{"libfoo.so.1.2.3", "libfoo.so.2.0.0"},
		{"libfoo.so.1", "libbar.so.1"},
		{"foo.1.2.3.so", "foo.4.5.6.so"},
		{"app-1.0.0-r5", "app-2.0.0-r0"},
		{"README.md", "README.md"},
		{"a.txt", "b.txt"},
		{"binary", "binary"},
		{"foo.jar", "bar.jar"},
		{"file.go", "file.go"},
		{"lib.rs", "mod.rs"},
		{"0xdeadbeef.py", "0xffffffff.py"},
		{"abc.upx", "abc.upx"},
		{"archive.tar.gz", "archive.tar.gz"},
	}

	for _, c := range cases {
		f.Add(c.a, c.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		res := Diff([]string{a}, []string{b})

		if res == nil {
			t.Fatalf("result is unexpectedly nil")
		}
	})
}

func FuzzEqual(f *testing.F) {
	cases := []struct {
		a, b string
	}{
		{"libfoo.so.1.2.3", "libfoo.so.2.0.0"},
		{"libfoo.so.1", "libbar.so.1"},
		{"foo.1.2.3.so", "foo.4.5.6.so"},
		{"app-1.0.0-r5", "app-2.0.0-r0"},
		{"README.md", "README.md"},
		{"a.txt", "b.txt"},
		{"binary", "binary"},
		{"foo.jar", "bar.jar"},
		{"file.go", "file.go"},
		{"lib.rs", "mod.rs"},
		{"0xdeadbeef.py", "0xffffffff.py"},
		{"abc.upx", "abc.upx"},
		{"archive.tar.gz", "archive.tar.gz"},
	}

	for _, c := range cases {
		f.Add(c.a, c.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		got := identity.Equal(a, b)

		if reflect.ValueOf(got).Kind().String() != "bool" {
			t.Errorf("unexpected equal type: %T", got)
		}
	})
}

func FuzzSoname(f *testing.F) {
	cases := []struct {
		input string
	}{
		{"libfoo.so.1"},
		{"libfoo.so.1.2.3"},
		{"libz.so.1"},
		{"libssl.so.3"},
		{"usr/lib/libcrypto.so.1.1.0"},
		{"libfoo.so"},
		{"foo.txt"},
		{".so.1"},
	}

	for _, c := range cases {
		f.Add(c.input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got := identity.Soname([]byte(input))

		if got < 0 {
			t.Errorf("Soname(%q) = %q less than 0", input, got)
		}
	})
}

func FuzzScript(f *testing.F) {
	cases := []struct {
		input string
	}{
		{"alpine-baselayout-3.6.8-r1.Q17OteNVXn9.post-install"},
		{"busybox-1.37.0-r12.Q1sSNCl4MTQ0.trigger"},
		{"foo-1.0.Q1xxx.pre-install"},
		{"foo-1.0.Q1xxx.pre-upgrade"},
		{"foo-1.0.Q1xxx.post-upgrade"},
		{"usr/bin/ls"},
		{"foo.post-install"},
		{"foo-1.0.Q1xxx.txt"},
	}

	for _, c := range cases {
		f.Add(c.input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i, j := identity.Script([]byte(input))

		if i < 0 || j < 0 {
			t.Errorf("Script(%q) = (%q, %q) less than 0", input, i, j)
		}
	})
}

func FuzzSuffix(f *testing.F) {
	cases := []struct {
		input string
	}{
		{"app-1.0.0"},
		{"app-1.0.0-r5"},
		{"tool-2.3.4-beta1"},
		{"python-3.11"},
		{"usr/bin/ls"},
		{"foo"},
		{"foo-bar"},
		{"bar-beta-p1"},
		{"sudo-x.y.z-p0"},
	}

	for _, c := range cases {
		f.Add(c.input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i := identity.Suffix([]byte(input))

		if i < 0 {
			t.Errorf("Suffix(%q) = %q less than 0", input, i)
		}
	})
}

func FuzzEmbedded(f *testing.F) {
	cases := []struct {
		input string
	}{
		{"foo.1.2.3.so"},
		{"bar.4.5.6.dylib"},
		{"libfoo.1.2.3.4.so"},
		{"foo.so"},
		{"foo.1.so"},
		{"foo.txt"},
		{"go.mod"},
		{"README.md"},
		{"man_page.7"},
	}

	for _, c := range cases {
		f.Add(c.input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		i := identity.Suffix([]byte(input))

		if i < 0 {
			t.Errorf("Embedded(%q) = %q less than 0", input, i)
		}
	})
}
