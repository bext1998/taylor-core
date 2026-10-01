package config

import (
	"bytes"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeCredentialBlob turns a Credential Manager blob into the key text.
//
// Brunel's own writer stores UTF-8, but `cmdkey /pass:` and the Windows
// credential UI store the password as UTF-16LE. Read as UTF-8 that is the key
// with a NUL after every character; injected into an environment block it
// makes CreateProcess fail with an error that does not say why. So a blob that
// is not clean UTF-8 is tried as UTF-16LE, and anything that is still not
// plain text is rejected with ErrCredentialInvalid rather than passed on.
func decodeCredentialBlob(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", ErrCredentialInvalid
	}
	if utf8.Valid(blob) && bytes.IndexByte(blob, 0) < 0 {
		return string(blob), nil
	}
	if len(blob)%2 != 0 {
		return "", ErrCredentialInvalid
	}
	units := make([]uint16, len(blob)/2)
	for i := range units {
		units[i] = uint16(blob[2*i]) | uint16(blob[2*i+1])<<8
	}
	text := string(utf16.Decode(units))
	// An unpaired surrogate decodes to U+FFFD, which no real key contains.
	if !utf8.ValidString(text) || bytes.ContainsRune([]byte(text), 0) || bytes.ContainsRune([]byte(text), utf8.RuneError) {
		return "", ErrCredentialInvalid
	}
	return text, nil
}
