//go:build linux

package device

import "testing"

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"abc":      `'abc'`,
		"a b":      `'a b'`,
		"":         `''`,
		"a'b":      `'a'\''b'`,           // single quote must break out and re-enter
		"'; reboot": `''\''; reboot'`,    // injection attempt stays one literal arg
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
