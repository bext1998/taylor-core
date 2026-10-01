// Package e2e holds the three fixed real-task fixtures that close Alpha 1
// (spec.md AC-16, TC-E2E): a bug fix, a small feature and a failing-test
// diagnosis. Each fixture is a tiny Go project with a task, the command that
// verifies it, and a reference solution proving it can be solved.
//
// This is deliberately not a benchmark framework: no runner, aggregation,
// comparison or budgets. The default tests only prove the fixtures are sound;
// running a model against them is opt-in (see real_run_windows_test.go),
// because default CI must not need a real provider.
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

type fixture struct {
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Task           string   `json:"task"`
	Verify         []string `json:"verify"`
	ProtectedFiles []string `json:"protected_files"`

	dir string
}

func (f fixture) project() string  { return filepath.Join(f.dir, "project") }
func (f fixture) solution() string { return filepath.Join(f.dir, "solution") }

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	entries, err := os.ReadDir("fixtures")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var out []fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join("fixtures", e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		var f fixture
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("%s: fixture.json: %v", e.Name(), err)
		}
		f.dir = dir
		out = append(out, f)
	}
	return out
}

// copyTree copies the directory src into dst (created if needed).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

// hashTree fingerprints every file under dir (path and content), so a test
// can prove a fixture's source was not changed by a run on its copy.
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", dir, err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("hash %s: %v", rel, err)
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// verify runs the fixture's verification command in dir and reports whether
// it exited zero.
func (f fixture) verifyPasses(t *testing.T, dir string) (bool, string) {
	t.Helper()
	cmd := exec.Command(f.Verify[0], f.Verify[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// The three categories AC-16 names must all be present, each complete enough
// to run.
func TestFixturesCoverTheThreeRealTaskKinds(t *testing.T) {
	fixtures := loadFixtures(t)
	want := map[string]bool{"bug-fix": false, "small-feature": false, "failing-test-diagnosis": false}
	for _, f := range fixtures {
		if _, ok := want[f.Name]; !ok {
			t.Errorf("unexpected fixture %q", f.Name)
			continue
		}
		want[f.Name] = true
		if f.Task == "" || len(f.Verify) == 0 || len(f.ProtectedFiles) == 0 {
			t.Errorf("%s: task, verify command and protected files are all required", f.Name)
		}
		for _, p := range f.ProtectedFiles {
			if _, err := os.Stat(filepath.Join(f.project(), p)); err != nil {
				t.Errorf("%s: protected file %s is missing from the project", f.Name, p)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("fixture %q is missing", name)
		}
	}
}

// A fixture is only a meaningful test if the task is real (verification fails
// before the work) and solvable (it passes with the reference solution)
// without touching the tests the agent is told not to edit.
func TestFixturesFailFirstAndPassWithTheReferenceSolution(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			sourceBefore := hashTree(t, f.dir)
			ws := t.TempDir()
			copyTree(t, f.project(), ws)

			if ok, out := f.verifyPasses(t, ws); ok {
				t.Fatalf("verification already passes before any work:\n%s", out)
			}

			protectedBefore := map[string]string{}
			for _, p := range f.ProtectedFiles {
				data, _ := os.ReadFile(filepath.Join(ws, p))
				protectedBefore[p] = string(data)
			}
			copyTree(t, f.solution(), ws)
			if ok, out := f.verifyPasses(t, ws); !ok {
				t.Fatalf("verification still fails with the reference solution:\n%s", out)
			}
			for p, before := range protectedBefore {
				after, _ := os.ReadFile(filepath.Join(ws, p))
				if string(after) != before {
					t.Errorf("the reference solution edits protected file %s", p)
				}
			}
			if hashTree(t, f.dir) != sourceBefore {
				t.Fatal("running on the copy changed the fixture source")
			}
		})
	}
}
