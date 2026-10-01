package pirpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SupportedPiVersion must match package.json and package-lock.json. An upgrade
// requires revalidating the RPC bridge; accepting arbitrary 0.x releases is unsafe.
const SupportedPiVersion = "0.85.1"

// Resolve relative extension paths against the workspace, never the host's cwd.
// The extension's installation takes precedence over the host executable's.
func resolvePiPath(opts LaunchOptions, workDir string) (string, error) {
	extension := strings.TrimSpace(opts.ExtensionPath)
	if extension == "" {
		extension = defaultExtensionPath
	}
	if !filepath.IsAbs(extension) {
		extension = filepath.Join(workDir, extension)
	}
	roots := []string{filepath.Dir(extension)}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	var searched []string
	for _, root := range roots {
		packageDir, err := filepath.Abs(filepath.Join(root, "node_modules", "@earendil-works", "pi-coding-agent"))
		if err != nil {
			return "", codeError(ErrPiRuntimeRequired.Code, "cannot resolve local Pi installation", err)
		}
		searched = append(searched, packageDir)
		if _, err := os.Stat(packageDir); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", codeError(ErrPiRuntimeRequired.Code, "cannot inspect local Pi installation; run npm ci", err)
		}
		// A broken local installation must fail, not silently select global Pi.
		return piPackageEntry(packageDir)
	}
	path, err := exec.LookPath("pi")
	if err != nil {
		return "", codeError(ErrPiRuntimeRequired.Code, "Pi was not found; searched "+strings.Join(searched, ", ")+" and PATH; run npm ci in the Brunel installation directory", err)
	}
	return path, nil
}

// CreateProcessW cannot execute npm's Windows batch shims. Read the package's
// declared bin instead and invoke Node directly, preserving argument boundaries
// and the RPC launcher's Job Object. Never interpret a shim with cmd.exe.
func piInvocation(path string) (string, []string, error) {
	if strings.EqualFold(filepath.Ext(path), ".js") || strings.EqualFold(filepath.Ext(path), ".mjs") || strings.EqualFold(filepath.Ext(path), ".cjs") {
		node, err := exec.LookPath("node")
		if err != nil {
			return "", nil, codeError(ErrPiRuntimeRequired.Code, "Node.js is required to launch Pi", err)
		}
		return node, []string{path}, nil
	}
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path, nil, nil
	}
	dir := filepath.Dir(path)
	modules := filepath.Join(dir, "node_modules") // npm global shim layout
	if filepath.Base(dir) == ".bin" {
		modules = filepath.Dir(dir)
	}
	packageDir := filepath.Join(modules, "@earendil-works", "pi-coding-agent")
	entry, err := piPackageEntry(packageDir)
	if err != nil {
		return "", nil, err
	}
	node := filepath.Join(dir, "node.exe") // match npm's adjacent-node preference
	if info, err := os.Stat(node); err != nil || info.IsDir() {
		node, err = exec.LookPath("node")
		if err != nil {
			return "", nil, codeError(ErrPiRuntimeRequired.Code, "Node.js is required to launch Pi", err)
		}
	}
	return node, []string{entry}, nil
}

func piPackageEntry(packageDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
	var manifest struct {
		Bin map[string]string `json:"bin"`
	}
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	if err != nil || manifest.Bin["pi"] == "" {
		return "", codeError(ErrPiRuntimeRequired.Code, "cannot resolve npm Pi entry point; run npm ci", err)
	}
	entry := filepath.Join(packageDir, manifest.Bin["pi"])
	info, err := os.Stat(entry)
	if err != nil || info.IsDir() {
		return "", codeError(ErrPiRuntimeRequired.Code, "Pi entry point is missing; run npm ci", err)
	}
	return entry, nil
}

// versionProbeTimeout bounds `pi --version`; a variable so tests can shorten it.
var versionProbeTimeout = 10 * time.Second

func checkPiVersion(ctx context.Context, path string, prefix []string, workDir string) error {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	args := append(append([]string(nil), prefix...), "--version")
	// The probe inherits the host environment, but does not receive the RPC
	// launch's additional credential or approval-token injection. Never expose
	// arbitrary stdout/stderr in a public error. runVersionProbe also owns the
	// probe's whole process tree: on every exit path nothing it started is left
	// running (INV-7).
	output := &versionOutput{}
	if err := runVersionProbe(ctx, path, args, workDir, output); err != nil {
		return codeError(ErrPiRuntimeRequired.Code, "cannot query Pi version; run npm ci and verify Node.js", err)
	}
	if output.overflow || strings.TrimSpace(string(output.data)) != SupportedPiVersion {
		return codeError(ErrPiVersionMismatch.Code, fmt.Sprintf("Pi version is not supported; expected %s; run npm ci", SupportedPiVersion), nil)
	}
	return nil
}

type versionOutput struct {
	data     []byte
	overflow bool
}

func (w *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 256 - len(w.data)
	if len(p) > remaining {
		w.overflow = true
		p = p[:remaining]
	}
	w.data = append(w.data, p...)
	return n, nil
}
