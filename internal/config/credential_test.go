package config

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16LE(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// `cmdkey /pass:` stores UTF-16LE. The key must come back as the text the
// user typed, with no NULs that would later break the Pi subprocess launch.
func TestDecodeCredentialBlobReadsCmdkeyUTF16(t *testing.T) {
	const key = "sk-or-v1-0123456789abcdef"
	got, err := decodeCredentialBlob(utf16LE(key))
	if err != nil {
		t.Fatalf("decodeCredentialBlob(UTF-16) error = %v", err)
	}
	if got != key {
		t.Fatalf("decodeCredentialBlob(UTF-16) = %q, want %q", got, key)
	}
	if strings.ContainsRune(got, 0) {
		t.Fatal("decoded key still contains NUL")
	}
}

// Keys saved by Brunel's own writer are UTF-8 and must keep working.
func TestDecodeCredentialBlobReadsBrunelUTF8(t *testing.T) {
	const key = "sk-or-v1-0123456789abcdef"
	got, err := decodeCredentialBlob([]byte(key))
	if err != nil {
		t.Fatalf("decodeCredentialBlob(UTF-8) error = %v", err)
	}
	if got != key {
		t.Fatalf("decodeCredentialBlob(UTF-8) = %q, want %q", got, key)
	}
}

// A blob that is neither clean UTF-8 nor clean UTF-16 must be refused, never
// returned: a NUL-bearing key reaching the environment block is the bug.
func TestDecodeCredentialBlobRejectsUnreadableBlobs(t *testing.T) {
	cases := map[string][]byte{
		"empty":               {},
		"utf8 with nul":       []byte("abc\x00def"),
		"odd length with nul": {'a', 0, 'b'},
		"invalid utf8":        {0xff, 0xfe, 0xfd},
		"utf16 with nul":      utf16LE("ab\x00cd"),
		"utf16 unpaired lead": {0x00, 0xd8, 'a', 0},
	}
	for name, blob := range cases {
		got, err := decodeCredentialBlob(blob)
		if !errors.Is(err, ErrCredentialInvalid) {
			t.Errorf("%s: error = %v, want ErrCredentialInvalid", name, err)
		}
		if got != "" {
			t.Errorf("%s: returned %q alongside the error", name, got)
		}
	}
}
