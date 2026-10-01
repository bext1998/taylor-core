//go:build windows

package pirpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// The version probe is a child process like any other: whatever it starts
// must not outlive it (INV-7). Each case has the fake Pi start a grandchild
// that holds the probe's stdout, then end the probe a different way.
func TestVersionProbeReclaimsItsWholeProcessTree(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pi.exe")
	if out, err := exec.Command("go", "build", "-o", entry, "./testdata/fakepi").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	pidFile := filepath.Join(root, "grandchild.pid")
	t.Setenv("FAKE_PI_PIDFILE", pidFile)

	plain := func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }
	cases := []struct {
		name    string
		mode    string
		timeout time.Duration // versionProbeTimeout; 0 keeps the default
		ctx     func() (context.Context, context.CancelFunc)
		wantErr bool
	}{
		{"normal exit leaves a grandchild", "exit", 0, plain, false},
		{"probe timeout", "hang", time.Second, plain, true},
		{"caller cancellation", "hang", 0, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 1500*time.Millisecond)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(pidFile)
			t.Setenv("FAKE_PI_VERSION_TREE", c.mode)
			if c.timeout > 0 {
				old := versionProbeTimeout
				versionProbeTimeout = c.timeout
				defer func() { versionProbeTimeout = old }()
			}
			ctx, cancel := c.ctx()
			defer cancel()

			start := time.Now()
			err := checkPiVersion(ctx, entry, nil, root)
			if elapsed := time.Since(start); elapsed > 8*time.Second {
				t.Fatalf("probe took %v", elapsed)
			}
			if (err != nil) != c.wantErr {
				t.Fatalf("checkPiVersion error = %v, wantErr %v", err, c.wantErr)
			}

			data, rerr := os.ReadFile(pidFile)
			if rerr != nil {
				t.Fatalf("the grandchild never started: %v", rerr)
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			deadline := time.Now().Add(3 * time.Second)
			for processAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if processAlive(pid) {
				if h, err := syscall.OpenProcess(0x0001 /* PROCESS_TERMINATE */, false, uint32(pid)); err == nil {
					_ = syscall.TerminateProcess(h, 1)
					syscall.CloseHandle(h)
				}
				t.Fatalf("grandchild %d outlived the version probe", pid)
			}
		})
	}
}
