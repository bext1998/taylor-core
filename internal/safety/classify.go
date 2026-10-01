package safety

import (
	"regexp"
	"strings"
	"unicode"
)

// classify decides AUTO vs CONFIRM for one tool call (spec.md §6.2).
// Every file tool - list_files, search_text, read_file, workspace_diff,
// apply_patch, create_file, write_file - is always AUTO: reads carry no
// risk, and writes are already exact and hash-guarded elsewhere
// (workspace + internal/filetools own that precondition, Issue #5). Only
// run_powershell's command text is inspected against the six
// representative CONFIRM categories from spec.md §6.2, checked in the
// order they are listed there; the first match wins and its category name
// becomes the reason shown in the approval prompt.
func (g *Gate) classify(call ToolCall) (Risk, string) {
	if call.Tool != "run_powershell" {
		return RiskAuto, ""
	}
	if reason, confirm := classifyPowerShell(call.Command, g.WorkspaceRoot); confirm {
		return RiskConfirm, reason
	}
	return RiskAuto, ""
}

// Representative command-form patterns. These are deliberately simple,
// best-effort token/substring checks - spec.md §6.2 explicitly disclaims
// full PowerShell semantic coverage ("不得宣稱涵蓋完整 PowerShell") and its
// test plan only requires the listed representative commands to classify
// correctly, not language completeness.
var (
	deleteVerbs      = map[string]bool{"remove-item": true, "ri": true, "rm": true, "del": true, "erase": true, "rd": true, "rmdir": true}
	deleteForceFlags = map[string]bool{"-recurse": true, "-force": true}
	clearVerbs       = map[string]bool{"clear-content": true, "clear-item": true}
	overwriteVerbs   = map[string]bool{"move-item": true, "copy-item": true}

	gitStateChangingSubcommands = map[string]bool{"commit": true, "push": true, "clean": true, "rebase": true}

	installTools    = map[string]bool{"npm": true, "npx": true, "pip": true, "pip3": true, "winget": true, "choco": true, "dotnet": true, "yarn": true, "pnpm": true}
	installVerbs    = map[string]bool{"install": true, "i": true, "add": true, "update": true, "upgrade": true, "ci": true}
	networkCommands = map[string]bool{"invoke-webrequest": true, "invoke-restmethod": true, "iwr": true, "irm": true, "curl": true, "curl.exe": true, "wget": true}
	backgroundVerbs = map[string]bool{"start-process": true, "start-job": true}

	// Matches a Windows drive-letter absolute path (either separator) or
	// a UNC share anywhere in the command text, e.g. C:\Users\x,
	// C:/Users/x or \\server\share.
	//
	// A comma or semicolon ends a path: `C:\ws\a.txt,C:\Windows\win.ini` is
	// two paths in one argument list, and each must be checked on its own.
	reAbsoluteWindowsPath = regexp.MustCompile(`(?i)[A-Z]:[\\/][^\s"'|,;]*|\\\\[^\s"'|,;]+`)
)

// classifyPowerShell returns (reason, true) if command matches one of the
// six representative CONFIRM categories, checked in spec order.
func classifyPowerShell(command, workspaceRoot string) (string, bool) {
	tokens := tokenize(command)

	if containsAny(tokens, deleteVerbs) && containsAny(tokens, deleteForceFlags) {
		return "recursive or forced delete", true
	}
	// A wildcard delete removes many files at once, the same bulk effect the
	// move/copy check below already confirms. The whole command is searched,
	// so `Get-ChildItem *.tmp | Remove-Item` is caught too.
	if containsAny(tokens, deleteVerbs) && strings.ContainsAny(command, "*?") {
		return "bulk delete (wildcard)", true
	}
	if containsAny(tokens, clearVerbs) {
		return "clears file content", true
	}
	if containsAny(tokens, overwriteVerbs) && (tokens["-force"] || strings.ContainsAny(command, "*?")) {
		return "bulk move or overwrite", true
	}
	// A copy/move onto an existing destination overwrites it even without
	// -Force (PowerShell Copy-Item/Move-Item replace writable existing
	// files). We cannot know from static text whether the destination
	// exists, so any copy-item/move-item whose destination argument names
	// a file (not a directory glob ending in a separator) is treated as
	// a potential overwrite and confirmed.
	if overwriteStatementNamesFile(command) {
		return "bulk move or overwrite", true
	}

	if tokens["git"] {
		if containsAny(tokens, gitStateChangingSubcommands) {
			return "git state-changing command", true
		}
		if tokens["reset"] && tokens["--hard"] {
			return "git state-changing command", true
		}
	}

	if containsAny(tokens, installTools) && containsAny(tokens, installVerbs) {
		return "installs or updates dependencies or packages", true
	}

	if containsAny(tokens, networkCommands) {
		return "network transmission command", true
	}

	if containsAny(tokens, backgroundVerbs) {
		return "background process or job", true
	}

	if path, ok := firstOutOfWorkspaceAbsolutePath(command, workspaceRoot); ok {
		return "references an absolute path outside the workspace: " + path, true
	}

	return "", false
}

// tokenize splits command on whitespace into a lowercase token set for
// membership checks. It is intentionally simple (no quote-aware parsing):
// classification here is best-effort, not a PowerShell parser.
//
// PowerShell statement separators (`;`, `|`, `&`, and newline) are treated
// as whitespace so that a flag glued to a separator by the shell's
// whitespace rules (e.g. `-Recurse;` in `Remove-Item .\build -Recurse;
// Write-Output done`) still matches its flag token. Trailing punctuation
// (`.,;:|&`) is likewise trimmed from both ends of every token.
func tokenize(command string) map[string]bool {
	tokens := make(map[string]bool)
	for _, f := range strings.FieldsFunc(command, isSeparator) {
		t := strings.ToLower(strings.Trim(f, `"'`))
		t = strings.Trim(t, `.,;:|&`)
		if t != "" {
			tokens[t] = true
		}
	}
	return tokens
}

// isSeparator reports whether r separates PowerShell statements or tokens:
// whitespace plus the statement separators `;`, `|` and `&`.
func isSeparator(r rune) bool {
	switch {
	case r == ';', r == '|', r == '&', unicode.IsSpace(r):
		return true
	}
	return false
}

func containsAny(tokens map[string]bool, set map[string]bool) bool {
	for t := range set {
		if tokens[t] {
			return true
		}
	}
	return false
}

// overwriteStatementNamesFile reports whether any single statement of the
// command is a copy/move whose destination names a file. Each statement is
// judged on its own, so a harmless statement chained after a move is not
// mistaken for the move's destination, and a move later in the chain is
// still examined.
func overwriteStatementNamesFile(command string) bool {
	statements := strings.FieldsFunc(command, func(r rune) bool { return r == ';' || r == '|' || r == '&' || r == '\n' })
	for _, statement := range statements {
		if containsAny(tokenize(statement), overwriteVerbs) && hasFileDestination(statement) {
			return true
		}
	}
	return false
}

// hasFileDestination reports whether a copy-item/move-item command's
// destination argument names a file rather than a directory. A trailing
// path separator (e.g. `Move-Item .\a.txt .\archive\`) means "into that
// directory", which is a rename, not an overwrite of a named file.
// Best-effort: it inspects the last non-flag argument of the command.
func hasFileDestination(command string) bool {
	fields := strings.Fields(command)
	for i := len(fields) - 1; i >= 0; i-- {
		f := strings.Trim(fields[i], `"'`)
		if f == "" {
			continue
		}
		if strings.HasPrefix(f, "-") {
			// Skip flags and their values conservatively: once we hit a
			// flag scanning backwards, the remaining earlier fields are
			// the source arguments, so there is no explicit file
			// destination and the overwrite check does not fire.
			return false
		}
		return !strings.HasSuffix(f, `\`) && !strings.HasSuffix(f, `/`)
	}
	return false
}

// firstOutOfWorkspaceAbsolutePath returns the first absolute Windows path
// found in command that does not fall under workspaceRoot. An empty
// workspaceRoot disables this check (nothing to compare against).
func firstOutOfWorkspaceAbsolutePath(command, workspaceRoot string) (string, bool) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", false
	}
	root := normalizeWindowsPath(workspaceRoot)
	for _, match := range reAbsoluteWindowsPath.FindAllString(command, -1) {
		normalized := normalizeWindowsPath(match)
		if !isWithinRoot(root, normalized) {
			return match, true
		}
	}
	return "", false
}

// normalizeWindowsPath canonicalizes a Windows path for containment
// comparison: forward slashes become backslashes, and `..` segments are
// resolved lexically so `C:\ws\..\Windows` becomes `C:\Windows`. A path
// that escapes above a drive root (e.g. `C:\..`) is left as-is; it can
// never be within any workspace root anyway.
func normalizeWindowsPath(path string) string {
	p := strings.ReplaceAll(path, `/`, `\`)
	parts := strings.Split(strings.TrimRight(p, `\`), `\`)
	var stack []string
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 && stack[len(stack)-1] != ".." {
				stack = stack[:len(stack)-1]
				continue
			}
			// Escaping above the root: keep the ".." so the path stays
			// out-of-workspace by construction.
			stack = append(stack, "..")
		default:
			stack = append(stack, part)
		}
	}
	return strings.Join(stack, `\`)
}

func isWithinRoot(root, path string) bool {
	if strings.EqualFold(root, path) {
		return true
	}
	if len(path) <= len(root) || !strings.EqualFold(path[:len(root)], root) {
		return false
	}
	sep := path[len(root)]
	return sep == '\\' || sep == '/'
}
