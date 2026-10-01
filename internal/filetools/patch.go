package filetools

import (
	"fmt"
	"sort"
	"strings"
)

// Hunk is one exact, contiguous edit to a file's line range. StartLine and
// EndLine are 1-based and inclusive over the file's *current* lines;
// EndLine == StartLine-1 marks a pure insertion before StartLine with no
// lines removed. OldLines must equal the file's current lines in
// [StartLine, EndLine] exactly - ApplyPatch never attempts fuzzy or
// approximate context matching, nor any automatic merge - or the whole
// patch is rejected and the file is left unchanged.
type Hunk struct {
	StartLine int
	EndLine   int
	OldLines  []string
	NewLines  []string
}

// ApplyPatch applies one or more exact hunks to the file at path, but only
// when the file's current whole-file hash still matches expectedHash and
// every hunk's OldLines match the file's current content exactly. Any
// mismatch - stale hash, patch conflict, or invalid/overlapping hunks -
// leaves the target file byte-for-byte unchanged. On success it returns the
// new whole-file SHA-256 hash.
func ApplyPatch(r Resolver, path, expectedHash string, hunks []Hunk) (string, error) {
	if len(hunks) == 0 {
		return "", codeError(ErrInvalidArgument.Code, "at least one hunk is required", nil)
	}
	if strings.TrimSpace(expectedHash) == "" {
		return "", codeError(ErrInvalidArgument.Code, "expected_hash is required", nil)
	}
	abs, err := r.Resolve(path)
	if err != nil {
		return "", err
	}
	current, err := readExistingFile(abs)
	if err != nil {
		return "", err
	}
	if hashBytes(current) != expectedHash {
		return "", codeError(ErrStaleHash.Code, "file changed since it was last read", nil)
	}

	lines := splitLines(current)
	sorted := make([]Hunk, len(hunks))
	copy(sorted, hunks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartLine < sorted[j].StartLine })
	if err := validateHunks(sorted, len(lines)); err != nil {
		return "", err
	}
	newLines, err := applyHunks(lines, sorted)
	if err != nil {
		return "", err
	}

	newData := joinLines(newLines)
	if err := atomicReplace(abs, newData, expectedHash); err != nil {
		return "", err
	}
	return hashBytes(newData), nil
}

// validateHunks checks that every hunk targets a well-formed, in-range span
// and that hunks (sorted by StartLine) do not overlap or share an
// insertion point - all without ever reading file content, so a malformed
// request never triggers the context-matching pass in applyHunks.
func validateHunks(hunks []Hunk, total int) error {
	prevEnd := 0     // 0 means "before line 1": no line has been claimed yet
	prevStart := 0   // StartLine of the previous hunk (sorted order)
	for _, h := range hunks {
		if h.StartLine < 1 || h.StartLine > total+1 {
			return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk start_line %d is out of range", h.StartLine), nil)
		}
		if h.EndLine < h.StartLine-1 || h.EndLine > total {
			return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk end_line %d is out of range", h.EndLine), nil)
		}
		wantOld := h.EndLine - h.StartLine + 1
		if len(h.OldLines) != wantOld {
			return codeError(ErrInvalidArgument.Code, "hunk old_lines does not match the start_line/end_line span", nil)
		}
		if h.StartLine <= prevEnd {
			return codeError(ErrInvalidArgument.Code, "hunks overlap or share an insertion point", nil)
		}
		// Two hunks editing at the same start line - including two pure
		// insertions before the same line, or an insertion plus a
		// replacement of that line - are ambiguous regardless of sort
		// order, so reject them deterministically.
		if h.StartLine == prevStart {
			return codeError(ErrInvalidArgument.Code, "hunks overlap or share an insertion point", nil)
		}
		prevEnd = h.EndLine
		prevStart = h.StartLine
	}
	return nil
}

// applyHunks builds the patched line sequence, verifying each hunk's exact
// context as it goes. It never merges or approximates: a single mismatched
// line aborts the whole patch before any output is produced.
func applyHunks(lines []rawLine, hunks []Hunk) ([]rawLine, error) {
	term := fileTerminator(lines)
	noTrailingNewline := len(lines) > 0 && lines[len(lines)-1].term == ""

	result := make([]rawLine, 0, len(lines))
	cursor := 1 // next original 1-based line number not yet copied
	for hi, h := range hunks {
		for cursor < h.StartLine {
			result = append(result, lines[cursor-1])
			cursor++
		}
		// When new lines are appended after a final line that had no
		// trailing newline, the copied final line needs a terminator so
		// the inserted lines start on their own line instead of being
		// glued onto it (e.g. "one" + EOF insert "two" must produce
		// "one\ntwo\n", not "onetwo\n").
		if noTrailingNewline && len(result) > 0 && len(h.NewLines) > 0 && result[len(result)-1].term == "" {
			result[len(result)-1].term = term
		}
		for i, want := range h.OldLines {
			actual := lines[h.StartLine-1+i].text
			if actual != want {
				return nil, codeError(ErrPatchConflict.Code, fmt.Sprintf("hunk context does not match file content at line %d", h.StartLine+i), nil)
			}
		}
		for _, text := range h.NewLines {
			result = append(result, rawLine{text: text, term: term})
		}
		// Preserve a file that had no trailing newline when the last hunk
		// replaces through the true end of file. A pure insertion at EOF
		// (EndLine == StartLine-1) does not consume the final line, so the
		// inserted lines keep normal terminators.
		if hi == len(hunks)-1 && h.EndLine >= h.StartLine && h.EndLine == len(lines) && noTrailingNewline && len(h.NewLines) > 0 {
			result[len(result)-1].term = ""
		}
		cursor = h.EndLine + 1
	}
	for cursor <= len(lines) {
		result = append(result, lines[cursor-1])
		cursor++
	}
	return result, nil
}
