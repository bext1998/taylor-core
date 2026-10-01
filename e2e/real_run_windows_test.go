//go:build windows

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bext1998/brunel/internal/completion"
)

// TestFixturesWithARealModel is the AC-16 closure check: for each fixture it
// copies the project to a temporary workspace, runs the standard CLI
// (`brunel "<task>" --report ...`) against a real model, and checks the
// verification command passes, the report is complete and truthful, the
// protected tests are untouched and the fixture source did not change.
//
// It needs a real provider, so it is opt-in and never runs in default CI:
//
//	BRUNEL_E2E_EXE    path to a built brunel.exe (next to taylor-tools.ts and
//	                  the pinned Pi, i.e. built in the repository root)
//	BRUNEL_E2E_MODEL  the --model value, e.g. openrouter/anthropic/claude-haiku-4.5
func TestFixturesWithARealModel(t *testing.T) {
	exe, model := os.Getenv("BRUNEL_E2E_EXE"), os.Getenv("BRUNEL_E2E_MODEL")
	if exe == "" || model == "" {
		t.Skip("set BRUNEL_E2E_EXE and BRUNEL_E2E_MODEL to run against a real model")
	}
	for _, f := range loadFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			sourceBefore := hashTree(t, f.dir)
			ws := t.TempDir()
			copyTree(t, f.project(), ws)
			// A git workspace so the report can carry a diff.
			for _, args := range [][]string{
				{"init", "-q"},
				{"config", "user.email", "e2e@example.com"},
				{"config", "user.name", "e2e"},
				{"add", "."},
				{"commit", "-q", "-m", "fixture"},
			} {
				if out, err := exec.Command("git", append([]string{"-C", ws}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			protected := map[string]string{}
			for _, p := range f.ProtectedFiles {
				data, _ := os.ReadFile(filepath.Join(ws, p))
				protected[p] = string(data)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "--mode", "workspace", "--model", model, "--report", "e2e-report.json", f.Task)
			cmd.Dir = ws
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("brunel failed: %v\n%s", err, out)
			}

			data, err := os.ReadFile(filepath.Join(ws, "e2e-report.json"))
			if err != nil {
				t.Fatalf("no report: %v", err)
			}
			var rep completion.Report
			if err := json.Unmarshal(data, &rep); err != nil {
				t.Fatalf("report is not valid JSON: %v", err)
			}
			if rep.SchemaVersion != completion.SchemaVersion || rep.Status != completion.StatusCompleted {
				t.Fatalf("report schema/status = %q/%q", rep.SchemaVersion, rep.Status)
			}
			if len(rep.ModifiedFiles) == 0 || rep.Diff == "" || len(rep.Verifications) == 0 {
				t.Fatalf("report lacks the facts of a closed loop: modified=%v diff=%d bytes verifications=%d",
					rep.ModifiedFiles, len(rep.Diff), len(rep.Verifications))
			}

			if ok, vout := f.verifyPasses(t, ws); !ok {
				t.Fatalf("verification fails after the run:\n%s", vout)
			}
			for p, before := range protected {
				after, _ := os.ReadFile(filepath.Join(ws, p))
				if string(after) != before {
					t.Errorf("the run edited protected file %s", p)
				}
			}
			if hashTree(t, f.dir) != sourceBefore {
				t.Fatal("the run changed the fixture source")
			}
		})
	}
}
