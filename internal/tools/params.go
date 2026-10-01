package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// The parameter structs mirror the frozen schema in spec.md section 5.4.
// Optional fields are pointers so omission stays distinct from an explicit
// zero value; required-field presence is checked by decodeParams.
type ListFilesParams struct {
	Path     string  `json:"path"`
	Glob     *string `json:"glob,omitempty"`
	MaxDepth *int    `json:"max_depth,omitempty"`
}

type SearchTextParams struct {
	Pattern    string  `json:"pattern"`
	Path       *string `json:"path,omitempty"`
	Glob       *string `json:"glob,omitempty"`
	MaxResults *int    `json:"max_results,omitempty"`
}

type ReadFileParams struct {
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

type PatchHunk struct {
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	OldLines  []string `json:"old_lines"`
	NewLines  []string `json:"new_lines"`
}

type ApplyPatchParams struct {
	Path         string      `json:"path"`
	ExpectedHash string      `json:"expected_hash"`
	Hunks        []PatchHunk `json:"hunks"`
}

type CreateFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type WriteFileParams struct {
	Path         string `json:"path"`
	ExpectedHash string `json:"expected_hash"`
	Content      string `json:"content"`
}

type RunPowerShellParams struct {
	Command    string  `json:"command"`
	TimeoutSec *int64  `json:"timeout_sec,omitempty"`
	CWD        *string `json:"cwd,omitempty"`
}

type WorkspaceDiffParams struct {
	Path *string `json:"path,omitempty"`
}

func decodeParams(raw json.RawMessage, dst any, required ...string) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return codeError(ErrInvalidArgument.Code, "parameters must be a JSON object", nil)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return codeError(ErrInvalidArgument.Code, "parameters must be a JSON object", err)
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return codeError(ErrInvalidArgument.Code, fmt.Sprintf("parameter %q must not be null", name), nil)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return codeError(ErrInvalidArgument.Code, "parameters do not match the tool schema", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return codeError(ErrInvalidArgument.Code, "parameters contain trailing JSON values", nil)
		}
		return codeError(ErrInvalidArgument.Code, "parameters contain invalid trailing data", err)
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return codeError(ErrInvalidArgument.Code, fmt.Sprintf("parameter %q is required", name), nil)
		}
	}
	return nil
}

func validatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return codeError(ErrInvalidArgument.Code, "path is required", nil)
	}
	return nil
}

func validateGlob(glob *string) error {
	if glob == nil {
		return nil
	}
	if _, err := filepath.Match(*glob, ""); err != nil {
		return codeError(ErrInvalidArgument.Code, "glob is invalid", err)
	}
	return nil
}

func validateListFilesParams(p ListFilesParams) error {
	if err := validatePath(p.Path); err != nil {
		return err
	}
	if p.MaxDepth != nil && *p.MaxDepth < 0 {
		return codeError(ErrInvalidArgument.Code, "max_depth must not be negative", nil)
	}
	return validateGlob(p.Glob)
}

func validateSearchTextParams(p SearchTextParams) error {
	if p.Path != nil {
		if err := validatePath(*p.Path); err != nil {
			return err
		}
	}
	if p.MaxResults != nil && *p.MaxResults < 0 {
		return codeError(ErrInvalidArgument.Code, "max_results must not be negative", nil)
	}
	return validateGlob(p.Glob)
}

func validateReadFileParams(p ReadFileParams) error {
	if err := validatePath(p.Path); err != nil {
		return err
	}
	if p.StartLine != nil && *p.StartLine < 0 {
		return codeError(ErrInvalidArgument.Code, "start_line must not be negative", nil)
	}
	if p.EndLine != nil && *p.EndLine < 0 {
		return codeError(ErrInvalidArgument.Code, "end_line must not be negative", nil)
	}
	return nil
}

func validateApplyPatchParams(p ApplyPatchParams) error {
	if err := validatePath(p.Path); err != nil {
		return err
	}
	if strings.TrimSpace(p.ExpectedHash) == "" {
		return codeError(ErrInvalidArgument.Code, "expected_hash is required", nil)
	}
	return nil
}

func decodeApplyPatchParams(raw json.RawMessage, p *ApplyPatchParams) error {
	if err := decodeParams(raw, p, "path", "expected_hash", "hunks"); err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return codeError(ErrInvalidArgument.Code, "parameters do not match the tool schema", err)
	}
	var hunks []map[string]json.RawMessage
	if err := json.Unmarshal(top["hunks"], &hunks); err != nil {
		return codeError(ErrInvalidArgument.Code, "hunks do not match the tool schema", err)
	}
	for index, hunk := range hunks {
		if hunk == nil {
			return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk %d must be an object", index), nil)
		}
		for _, name := range []string{"start_line", "end_line", "old_lines", "new_lines"} {
			value, ok := hunk[name]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk %d parameter %q is required", index, name), nil)
			}
		}
		// A null inside the line arrays would be coerced to "" by the JSON
		// decoder; the frozen schema says strings, so it is rejected instead of
		// silently filled in (CT-3).
		for _, name := range []string{"old_lines", "new_lines"} {
			var lines []json.RawMessage
			if err := json.Unmarshal(hunk[name], &lines); err != nil {
				return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk %d parameter %q must be an array of strings", index, name), err)
			}
			for _, line := range lines {
				if bytes.Equal(bytes.TrimSpace(line), []byte("null")) {
					return codeError(ErrInvalidArgument.Code, fmt.Sprintf("hunk %d parameter %q must not contain null", index, name), nil)
				}
			}
		}
	}
	return nil
}

func validateCreateFileParams(p CreateFileParams) error { return validatePath(p.Path) }

func validateWriteFileParams(p WriteFileParams) error {
	if err := validatePath(p.Path); err != nil {
		return err
	}
	if strings.TrimSpace(p.ExpectedHash) == "" {
		return codeError(ErrInvalidArgument.Code, "expected_hash is required", nil)
	}
	return nil
}

func validateRunPowerShellParams(p RunPowerShellParams) error {
	if strings.TrimSpace(p.Command) == "" {
		return codeError(ErrInvalidArgument.Code, "command is required", nil)
	}
	if p.TimeoutSec != nil {
		if *p.TimeoutSec <= 0 || *p.TimeoutSec > math.MaxInt64/int64(time.Second) {
			return codeError(ErrInvalidArgument.Code, "timeout_sec must be a positive whole number of seconds", nil)
		}
	}
	if p.CWD != nil {
		return validatePath(*p.CWD)
	}
	return nil
}
