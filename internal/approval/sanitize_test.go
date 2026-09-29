package approval

import "testing"

func TestSanitizeForDisplay(t *testing.T) {
	cases := map[string]string{
		"git status\n\tok":   "git status\n\tok",
		"safe\rrm -rf x":     `safe\x0drm -rf x`,
		"a\x1b[2Jb":          `a\x1b[2Jb`,
		"a\x1b]0;title\x07b": `a\x1b]0;title\x07b`,
		"a\u202eb\u2066c":    `a\u202eb\u2066c`,
		"a\u009bb\x7f":       `a\u009bb\x7f`,
		"中文 ok":              "中文 ok",
		"bad\xffbyte":        `bad�byte`,
	}
	for in, want := range cases {
		if got := SanitizeForDisplay(in); got != want {
			t.Errorf("SanitizeForDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}
