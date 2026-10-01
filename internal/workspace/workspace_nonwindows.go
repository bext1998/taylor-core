//go:build !windows

package workspace

// Workspace is unavailable outside Brunel's supported Windows platform.
type Workspace struct{}

func Bind(string) (*Workspace, error) {
	return nil, codeError("E_UNSUPPORTED_PLATFORM", "workspace binding requires Windows", nil)
}

func (w *Workspace) Root() string { return "" }

func (w *Workspace) Identity() string { return "" }

func (w *Workspace) Resolve(string) (string, error) {
	return "", codeError("E_UNSUPPORTED_PLATFORM", "workspace resolution requires Windows", nil)
}
