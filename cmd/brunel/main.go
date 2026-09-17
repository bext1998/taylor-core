package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	brunelexec "github.com/bext1998/brunel/internal/exec"
	"github.com/bext1998/brunel/internal/safety"
	"github.com/bext1998/brunel/internal/tools"
	"github.com/bext1998/brunel/internal/workspace"
)

const (
	defaultMaxProcesses   = uint32(64)
	defaultMaxMemoryBytes = uint64(512 << 20)
	defaultMaxOutputBytes = int64(1 << 20)
)

var taylorToolNames = map[string]struct{}{
	"list_files":     {},
	"search_text":    {},
	"read_file":      {},
	"apply_patch":    {},
	"create_file":    {},
	"write_file":     {},
	"run_powershell": {},
	"workspace_diff": {},
}

type taylorToolConfig struct {
	name           string
	cwd            string
	mode           string
	timeout        time.Duration
	maxProcesses   uint
	maxMemoryBytes uint64
	maxOutputBytes int64
}

// AgentsMDContext is the near-directory AGENTS.md (issue #11, spec
// §7.3) for the path a successful tool call touched. taylor-tools.ts
// renders it into the tool result's content array so the model sees the
// rules that apply to that directory alongside the result. It is display
// context only: no safety, hash, or other decision-making code ever reads
// it, so AGENTS.md content cannot authorize a tool, change an
// AUTO/CONFIRM classification, or bypass a tool precondition.
type AgentsMDContext struct {
	// Source is the workspace-relative directory the AGENTS.md was read
	// from.
	Source  string `json:"source"`
	Content string `json:"content"`
}

type taylorToolResponse struct {
	Tool      string           `json:"tool"`
	Result    *tools.Result    `json:"result,omitempty"`
	Status    string           `json:"status"`
	ErrorCode string           `json:"error_code,omitempty"`
	Message   string           `json:"message,omitempty"`
	AgentsMD  *AgentsMDContext `json:"agents_md,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) int {
	if !isTaylorToolInvocation(args) {
		_, _ = fmt.Fprintln(diagnostics, "brunel: not yet implemented (see #2)")
		return 1
	}

	config, err := parseTaylorToolConfig(args, diagnostics)
	if err != nil {
		writeTaylorToolResponse(output, taylorToolResponse{
			Tool:      config.name,
			Status:    "error",
			ErrorCode: tools.ErrInvalidArgument.Code,
			Message:   responseMessage(tools.ErrInvalidArgument.Code),
		})
		return 1
	}
	return runTaylorTool(context.Background(), config, input, output)
}

func isTaylorToolInvocation(args []string) bool {
	for _, arg := range args {
		if arg == "--taylor-tool" || strings.HasPrefix(arg, "--taylor-tool=") {
			return true
		}
	}
	return false
}

func parseTaylorToolConfig(args []string, diagnostics io.Writer) (taylorToolConfig, error) {
	config := taylorToolConfig{
		mode:           "workspace",
		timeout:        120 * time.Second,
		maxProcesses:   uint(defaultMaxProcesses),
		maxMemoryBytes: defaultMaxMemoryBytes,
		maxOutputBytes: defaultMaxOutputBytes,
	}
	flags := flag.NewFlagSet("brunel", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.StringVar(&config.name, "taylor-tool", "", "run a frozen Taylor tool")
	flags.StringVar(&config.cwd, "cwd", "", "workspace directory")
	flags.StringVar(&config.mode, "mode", config.mode, "workspace or readonly")
	flags.DurationVar(&config.timeout, "timeout", config.timeout, "PowerShell timeout")
	flags.UintVar(&config.maxProcesses, "max-processes", config.maxProcesses, "PowerShell process limit")
	flags.Uint64Var(&config.maxMemoryBytes, "max-memory-bytes", config.maxMemoryBytes, "PowerShell per-process memory limit")
	flags.Int64Var(&config.maxOutputBytes, "max-output-bytes", config.maxOutputBytes, "PowerShell output limit per stream")
	if err := flags.Parse(args); err != nil {
		return config, err
	}
	if flags.NArg() != 0 || config.name == "" || !isTaylorToolName(config.name) {
		return config, tools.ErrInvalidArgument
	}
	if config.cwd == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return config, err
		}
		config.cwd = cwd
	}
	if config.mode != "workspace" && config.mode != "readonly" {
		return config, tools.ErrInvalidArgument
	}
	if config.timeout <= 0 || config.maxProcesses == 0 || config.maxProcesses > math.MaxUint32 || config.maxMemoryBytes == 0 || config.maxOutputBytes <= 0 {
		return config, tools.ErrInvalidArgument
	}
	return config, nil
}

func isTaylorToolName(name string) bool {
	_, ok := taylorToolNames[name]
	return ok
}

func runTaylorTool(ctx context.Context, config taylorToolConfig, input io.Reader, output io.Writer) int {
	params, err := io.ReadAll(input)
	if err != nil {
		return writeTaylorToolFailure(output, config.name, tools.ErrInvalidArgument.Code)
	}

	workspaceBinding, err := workspace.Bind(config.cwd)
	if err != nil {
		code := workspace.ErrorCode(err)
		if code == "" {
			code = workspace.ErrWorkspaceInvalid.Code
		}
		return writeTaylorToolFailure(output, config.name, code)
	}

	mode := safety.ModeWorkspace
	if config.mode == "readonly" {
		mode = safety.ModeReadonly
	}

	runner, runnerErr := brunelexec.NewRunner()
	registry := &tools.Registry{
		Workspace: workspaceBinding,
		Gate:      safety.NewGate(mode, nil, workspaceBinding.Root()),
		Runner:    runner,
		ExecLimits: tools.ExecLimits{
			DefaultTimeout: config.timeout,
			MaxProcesses:   uint32(config.maxProcesses),
			MaxMemoryBytes: config.maxMemoryBytes,
			MaxOutputBytes: config.maxOutputBytes,
		},
	}

	// This one-shot subprocess has no TTY or presentation layer. A CONFIRM
	// run_powershell call therefore fails closed with E_APPROVAL_REQUIRED_NO_TTY;
	// the approval UX belongs to issue #9's model-facing flow.
	result, err := registry.Call(ctx, config.name, json.RawMessage(params))
	if err != nil {
		if config.name == "run_powershell" && runner == nil && runnerErr != nil && tools.ErrorCode(err) == brunelexec.ErrUnsupportedPlatform.Code {
			err = runnerErr
		}
		code := tools.ErrorCode(err)
		if code == "" {
			code = tools.ErrToolIO.Code
		}
		return writeTaylorToolFailure(output, config.name, code)
	}

	response := taylorToolResponse{Tool: config.name, Result: &result, Status: "ok"}
	// Issue #11 (F-10): attach the AGENTS.md that applies to the path this
	// call touched. The lookup runs only after a successful call, reads
	// only, and feeds only the response - never the gate, hash guards, or
	// any other decision. A failed call carries no agents_md.
	if source, content, ok := nearAgentsMD(workspaceBinding, config.name, params); ok {
		response.AgentsMD = &AgentsMDContext{Source: source, Content: content}
	}
	writeTaylorToolResponse(output, response)
	return 0
}

func writeTaylorToolFailure(output io.Writer, tool, code string) int {
	writeTaylorToolResponse(output, taylorToolResponse{
		Tool:      tool,
		Status:    "error",
		ErrorCode: code,
		Message:   responseMessage(code),
	})
	return 1
}

func writeTaylorToolResponse(output io.Writer, response taylorToolResponse) {
	if err := json.NewEncoder(output).Encode(response); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "brunel: write response:", err)
	}
}

// agentsMDParam reports which JSON parameter carries the workspace path a
// tool call touches, if any. File targets are looked up from their
// containing directory; directory targets (list_files, workspace_diff,
// run_powershell cwd) from the directory itself. A call without the
// parameter falls back to the workspace-root AGENTS.md, which the initial
// prompt already carries.
func agentsMDParam(name string) (string, bool) {
	switch name {
	case "list_files", "search_text", "read_file", "apply_patch",
		"create_file", "write_file", "workspace_diff":
		return "path", true
	case "run_powershell":
		return "cwd", true
	}
	return "", false
}

// nearAgentsMD finds the AGENTS.md that applies to the path a tool call
// touched (issue #11, spec §7.3): starting from the target's directory
// and walking up to the workspace root, the nearest directory that
// contains an AGENTS.md wins (較近者優先). The walk stops before the
// workspace root itself: the root's AGENTS.md is already in the model's
// initial prompt, so re-sending it in every tool result would add no
// information. When no subdirectory level has an AGENTS.md, ok is false
// and the root rules remain the only ones in force (existing #9
// behavior).
//
// Every directory level is resolved through Workspace.Resolve, so the
// walk cannot follow a symlink, junction, or absolute path out of the
// workspace - the lookup adds no file-read channel that bypasses the
// existing escape protection. Any lookup failure returns ok=false: the
// lookup is display context and must never fail or alter a tool call.
//
// The content is re-read on every call; there is no cache, so an AGENTS.md
// added, modified, or removed between calls takes effect immediately.
//
// Known limitation (spec §7.3 says "read before operating"): --mode rpc
// has no pre-call context channel, so the nearest rule first becomes
// visible in the tool result that touches the directory; subsequent
// operations in the same directory then run with the rule in context.
func nearAgentsMD(w *workspace.Workspace, name string, params json.RawMessage) (string, string, bool) {
	param, ok := agentsMDParam(name)
	if !ok {
		return "", "", false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return "", "", false
	}
	raw, present := fields[param]
	if !present {
		return "", "", false
	}
	var rel string
	if err := json.Unmarshal(raw, &rel); err != nil || strings.TrimSpace(rel) == "" {
		return "", "", false
	}
	rel = filepath.Clean(rel)

	// Resolve the target through the workspace first: absolute paths,
	// ..-escapes, and anything that leaves the bound root fail here, as
	// they do for the tool itself.
	target, err := w.Resolve(rel)
	if err != nil {
		return "", "", false
	}

	// A file target's rules come from its directory; a directory target
	// (list_files, cwd, workspace_diff) from itself. A missing target
	// (create_file) is treated as a file.
	baseRel := rel
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		baseRel = rel
	} else {
		baseRel = filepath.Dir(rel)
	}

	// Walk from the nearest directory to the workspace root, resolving
	// every level through the workspace. filepath.Dir strictly shortens a
	// clean relative path until ".", so the loop always terminates.
	for {
		if baseRel == "." {
			return "", "", false
		}
		level, err := w.Resolve(baseRel)
		if err != nil {
			return "", "", false
		}
		candidate := filepath.Join(level, "AGENTS.md")
		// Use Lstat (not Stat) so a symlinked AGENTS.md pointing outside
		// the workspace is not followed (security: no new read channel).
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
			data, err := os.ReadFile(candidate)
			if err == nil {
				if content := strings.TrimSpace(string(data)); content != "" {
					return baseRel, content, true
				}
			}
		}
		parent := filepath.Dir(baseRel)
		if parent == baseRel {
			return "", "", false
		}
		baseRel = parent
	}
}

func responseMessage(code string) string {
	switch code {
	case tools.ErrInvalidArgument.Code:
		return "invalid tool request"
	case workspace.ErrWorkspaceInvalid.Code:
		return "workspace is invalid"
	case safety.ErrReadonlyMode.Code:
		return "tool is unavailable in readonly mode"
	case safety.ErrApprovalRequiredNoTTY.Code:
		return "approval requires an interactive TTY"
	case brunelexec.ErrPwshRequired.Code:
		return "pwsh (PowerShell 7+) is required"
	case brunelexec.ErrUnsupportedPlatform.Code:
		return "operation is unsupported on this platform"
	default:
		return "tool call failed"
	}
}
