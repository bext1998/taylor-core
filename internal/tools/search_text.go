package tools

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/bext1998/brunel/internal/filetools"
)

const (
	defaultSearchResults = 200
	// maxSearchResults caps max_results: a model asking for a million matches
	// still gets a bounded result.
	maxSearchResults = 1000
	// maxSearchFileBytes is the largest file search_text reads; a bigger file
	// is skipped instead of being loaded whole into memory.
	maxSearchFileBytes = 5 << 20
)

var errSearchLimit = errors.New("search result limit reached")

func searchText(r filetools.Resolver, pattern, path string, glob *string, maxResults *int) ([]TextMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, codeError(ErrInvalidArgument.Code, "pattern is not a valid regular expression", err)
	}
	root, err := r.Resolve(path)
	if err != nil {
		return nil, err
	}
	limit := defaultSearchResults
	if maxResults != nil {
		limit = min(*maxResults, maxSearchResults)
	}
	if limit == 0 {
		return []TextMatch{}, nil
	}
	base := filepath.Clean(path)
	matches := make([]TextMatch, 0)
	err = filepath.WalkDir(root, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if glob != nil {
			matched, err := filepath.Match(*glob, d.Name())
			if err != nil {
				return err
			}
			if !matched {
				return nil
			}
		}
		if info, err := d.Info(); err == nil && info.Size() > maxSearchFileBytes {
			return nil
		}
		data, binary, err := readTextFile(current)
		if err != nil {
			return err
		}
		if binary {
			return nil
		}
		if len(data) == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		displayPath := base
		if rel != "." {
			displayPath = filepath.Join(base, rel)
		}
		lines := bytes.Split(data, []byte("\n"))
		if bytes.HasSuffix(data, []byte("\n")) {
			lines = lines[:len(lines)-1]
		}
		for lineNumber, rawLine := range lines {
			line := bytes.TrimSuffix(rawLine, []byte("\r"))
			if !re.Match(line) {
				continue
			}
			matches = append(matches, TextMatch{Path: filepath.ToSlash(displayPath), Line: lineNumber + 1, Text: string(line)})
			if len(matches) >= limit {
				return errSearchLimit
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSearchLimit) {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, codeError(filetools.ErrNotFound.Code, "path does not exist", err)
		}
		return nil, codeError(ErrToolIO.Code, "cannot search workspace files", err)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Path < matches[j].Path
	})
	return matches, nil
}

func readTextFile(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	first := make([]byte, 8*1024)
	n, readErr := io.ReadFull(file, first)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, false, readErr
	}
	first = first[:n]
	if bytes.IndexByte(first, 0) >= 0 {
		return nil, true, nil
	}
	rest, err := io.ReadAll(io.LimitReader(file, maxSearchFileBytes))
	if err != nil {
		return nil, false, err
	}
	return append(first, rest...), false, nil
}
