//go:build windows

package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var getFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// Workspace is an immutable binding to one resolved Windows directory.
type Workspace struct {
	root      string
	finalRoot string
	identity  fileIdentity
}

type fileIdentity struct {
	volumeSerial uint32
	fileIndexHi  uint32
	fileIndexLo  uint32
}

// Bind resolves and validates a workspace root for the lifetime of a session.
func Bind(root string) (*Workspace, error) {
	if strings.TrimSpace(root) == "" || strings.IndexByte(root, 0) >= 0 {
		return nil, codeError(ErrWorkspaceInvalid.Code, "workspace root is empty or invalid", nil)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, codeError(ErrWorkspaceInvalid.Code, "cannot make workspace root absolute", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, codeError(ErrWorkspaceInvalid.Code, "cannot resolve workspace root", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("workspace root is not a directory")
		}
		return nil, codeError(ErrWorkspaceInvalid.Code, "workspace root is not a readable directory", err)
	}
	finalRoot, identity, err := finalPathAndIdentity(resolved)
	if err != nil {
		return nil, codeError(ErrWorkspaceInvalid.Code, "cannot inspect workspace root", err)
	}
	return &Workspace{root: resolved, finalRoot: finalRoot, identity: identity}, nil
}

// Identity returns a stable string naming the bound directory itself (volume
// and file index), not the path used to reach it. The host passes it to every
// tool-call process so each fresh binding can confirm it landed on the same
// directory the session started with (spec.md INV-5).
func (w *Workspace) Identity() string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%08x:%08x:%08x", w.identity.volumeSerial, w.identity.fileIndexHi, w.identity.fileIndexLo)
}

// Root returns the absolute, symlink-resolved workspace root.
func (w *Workspace) Root() string {
	if w == nil {
		return ""
	}
	return w.root
}

// Resolve returns the final path of an existing target, or the canonical path
// below the deepest existing ancestor for a new target.
func (w *Workspace) Resolve(path string) (string, error) {
	if w == nil {
		return "", codeError(ErrWorkspaceUnbound.Code, "workspace is not bound", nil)
	}
	if err := w.ensureBound(); err != nil {
		return "", err
	}
	if err := validateRelativePath(path); err != nil {
		return "", err
	}

	candidate := filepath.Join(w.root, filepath.Clean(path))
	ancestor, suffix, err := deepestExistingAncestor(candidate)
	if err != nil {
		return "", codeError(ErrPathEscape.Code, "cannot inspect path within workspace", err)
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", codeError(ErrPathEscape.Code, "cannot resolve path within workspace", err)
	}
	finalAncestor, _, err := finalPathAndIdentity(resolvedAncestor)
	if err != nil {
		return "", codeError(ErrPathEscape.Code, "cannot inspect resolved path within workspace", err)
	}
	if !containsPath(w.finalRoot, finalAncestor) {
		return "", codeError(ErrPathEscape.Code, "resolved path is outside workspace root", nil)
	}
	if len(suffix) == 0 {
		return finalAncestor, nil
	}
	parts := append([]string{finalAncestor}, suffix...)
	return filepath.Join(parts...), nil
}

func (w *Workspace) ensureBound() error {
	finalRoot, identity, err := finalPathAndIdentity(w.root)
	if err != nil || !sameIdentity(w.identity, identity) || !samePath(w.finalRoot, finalRoot) {
		return codeError(ErrWorkspaceUnbound.Code, "workspace root is no longer the bound directory", err)
	}
	return nil
}

func validateRelativePath(path string) error {
	if strings.IndexByte(path, 0) >= 0 || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return codeError(ErrPathEscape.Code, "path must be relative to workspace root", nil)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return codeError(ErrPathEscape.Code, "path escapes workspace root", nil)
	}
	return nil
}

func deepestExistingAncestor(candidate string) (string, []string, error) {
	current := candidate
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			return current, suffix, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("inspect workspace path: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, codeError(ErrPathEscape.Code, "path has no existing workspace ancestor", nil)
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func finalPathAndIdentity(path string) (string, fileIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fileIdentity{}, err
	}
	defer file.Close()
	connection, err := file.SyscallConn()
	if err != nil {
		return "", fileIdentity{}, err
	}

	var info syscall.ByHandleFileInformation
	var finalPath string
	var handleErr error
	err = connection.Control(func(handle uintptr) {
		if handleErr = syscall.GetFileInformationByHandle(syscall.Handle(handle), &info); handleErr != nil {
			return
		}
		finalPath, handleErr = finalPathFromHandle(syscall.Handle(handle))
	})
	if err != nil {
		return "", fileIdentity{}, err
	}
	if handleErr != nil {
		return "", fileIdentity{}, handleErr
	}
	if finalPath == "" {
		return "", fileIdentity{}, errors.New("cannot resolve final path from handle")
	}
	return finalPath, fileIdentity{volumeSerial: info.VolumeSerialNumber, fileIndexHi: info.FileIndexHigh, fileIndexLo: info.FileIndexLow}, nil
}

func finalPathFromHandle(handle syscall.Handle) (string, error) {
	buffer := make([]uint16, 1024)
	for {
		length, _, callErr := getFinalPathNameByHandle.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if length == 0 {
			if callErr != syscall.Errno(0) {
				return "", callErr
			}
			return "", errors.New("GetFinalPathNameByHandleW failed")
		}
		if int(length) < len(buffer) {
			return syscall.UTF16ToString(buffer[:length]), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func sameIdentity(a, b fileIdentity) bool {
	return a.volumeSerial == b.volumeSerial && a.fileIndexHi == b.fileIndexHi && a.fileIndexLo == b.fileIndexLo
}

func containsPath(root, target string) bool {
	root = strings.TrimRight(root, `\\/`)
	target = strings.TrimRight(target, `\\/`)
	if samePath(root, target) {
		return true
	}
	if len(target) <= len(root) || !strings.EqualFold(target[:len(root)], root) {
		return false
	}
	return target[len(root)] == '\\' || target[len(root)] == '/'
}

func samePath(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, `\\/`), strings.TrimRight(b, `\\/`))
}
