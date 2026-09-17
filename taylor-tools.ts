import { execFile } from "node:child_process";
import { promisify } from "node:util";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const execFileAsync = promisify(execFile);

// A Go-side response can legitimately exceed a few MB (read_file has no size
// cap, workspace_diff can be large, JSON escaping of control bytes expands up
// to ~6x). Keep this well above that worst case; the real fix is a stable
// Go-side response/size cap, tracked with the other internal/tools follow-ups.
const maxResponseBytes = 64 * 1024 * 1024;

type AgentsMDContext = {
  source: string;
  content: string;
};

type ToolResponse = {
  tool: string;
  status: string;
  result?: unknown;
  error_code?: string;
  message?: string;
  // Near-directory AGENTS.md (issue #11) for the path this call touched.
  // Display context only; never used for authorization or safety checks.
  agents_md?: AgentsMDContext;
};

function brunelExecutable(): string {
  const executable = process.env.BRUNEL_EXE;
  if (!executable) {
    throw new Error("BRUNEL_EXE is required to invoke Brunel tools");
  }
  return executable;
}

function brunelMode(): "workspace" | "readonly" {
  const value = process.env.BRUNEL_MODE;
  // Fail safe, not open: a spawner that forgets BRUNEL_MODE gets the
  // restricted mode rather than silently downgrading a --mode readonly
  // session to full write.
  if (value === undefined || value === "") {
    return "readonly";
  }
  if (value !== "workspace" && value !== "readonly") {
    throw new Error(`BRUNEL_MODE must be "workspace" or "readonly", got ${JSON.stringify(value)}`);
  }
  return value;
}

function decodeToolResponse(stdout: string): ToolResponse {
  return JSON.parse(stdout) as ToolResponse;
}

function assertToolOk(response: ToolResponse): ToolResponse {
  if (response.status === "error") {
    throw new Error(`${response.error_code ?? "E_TOOL_IO"}: ${response.message ?? "tool call failed"}`);
  }
  return response;
}

async function invokeTool(name: string, params: Record<string, unknown>, signal: AbortSignal | undefined, ctx: ExtensionContext) {
  const invocation = execFileAsync(brunelExecutable(), ["--taylor-tool", name, "--cwd", ctx.cwd, "--mode", brunelMode()], {
    cwd: ctx.cwd,
    encoding: "utf8",
    maxBuffer: maxResponseBytes,
    signal,
  });
  invocation.child.stdin?.end(JSON.stringify(params));
  try {
    const { stdout } = await invocation;
    return assertToolOk(decodeToolResponse(String(stdout)));
  } catch (error) {
    // A non-zero exit still carries the child's JSON error response on
    // error.stdout. Only recover from that when it is actually a non-empty
    // string; otherwise (ENOENT, AbortError, maxBuffer overflow, truncated
    // JSON) rethrow the real failure rather than masking it with a
    // SyntaxError that carries no stable code.
    const stdout = (error as { stdout?: unknown }).stdout;
    if (typeof stdout === "string" && stdout.trim() !== "") {
      let response: ToolResponse;
      try {
        response = decodeToolResponse(stdout);
      } catch {
        throw error;
      }
      return assertToolOk(response);
    }
    throw error;
  }
}

function agentsMDText(md: AgentsMDContext): string {
  return (
    `---Workspace agent instructions (AGENTS.md) — applies to: ${md.source}---\n` +
    md.content +
    `\n---end of AGENTS.md---`
  );
}

function executeTool(name: string) {
  return async (_toolCallId: string, params: Record<string, unknown>, signal: AbortSignal | undefined, _onUpdate: unknown, ctx: ExtensionContext) => {
    const response = await invokeTool(name, params, signal, ctx);
    const content: { type: "text"; text: string }[] = [
      { type: "text", text: JSON.stringify(response.result, null, 2) },
    ];
    if (response.agents_md) {
      content.push({ type: "text", text: agentsMDText(response.agents_md) });
    }
    return {
      content,
      details: response.result,
    };
  };
}

const registeredTools = [
  {
    name: "list_files",
    label: "List files",
    description: "List files in the current workspace.",
    parameters: Type.Object({
      path: Type.String(),
      glob: Type.Optional(Type.String()),
      max_depth: Type.Optional(Type.Integer()),
    }),
    execute: executeTool("list_files"),
  },
  {
    name: "search_text",
    label: "Search text",
    description: "Search text in the current workspace.",
    parameters: Type.Object({
      pattern: Type.String(),
      path: Type.Optional(Type.String()),
      glob: Type.Optional(Type.String()),
      max_results: Type.Optional(Type.Integer()),
    }),
    execute: executeTool("search_text"),
  },
  {
    name: "read_file",
    label: "Read file",
    description: "Read a file through Brunel.",
    parameters: Type.Object({
      path: Type.String(),
      start_line: Type.Optional(Type.Integer()),
      end_line: Type.Optional(Type.Integer()),
    }),
    execute: executeTool("read_file"),
  },
  {
    name: "apply_patch",
    label: "Apply patch",
    description: "Apply hash-guarded line hunks through Brunel.",
    parameters: Type.Object({
      path: Type.String(),
      expected_hash: Type.String(),
      hunks: Type.Array(Type.Object({
        start_line: Type.Integer(),
        end_line: Type.Integer(),
        old_lines: Type.Array(Type.String()),
        new_lines: Type.Array(Type.String()),
      })),
    }),
    execute: executeTool("apply_patch"),
  },
  {
    name: "create_file",
    label: "Create file",
    description: "Create a file through Brunel.",
    parameters: Type.Object({
      path: Type.String(),
      content: Type.String(),
    }),
    execute: executeTool("create_file"),
  },
  {
    name: "write_file",
    label: "Write file",
    description: "Write a hash-guarded file through Brunel.",
    parameters: Type.Object({
      path: Type.String(),
      expected_hash: Type.String(),
      content: Type.String(),
    }),
    execute: executeTool("write_file"),
  },
  {
    name: "run_powershell",
    label: "Run PowerShell",
    description: "Run PowerShell through Brunel.",
    parameters: Type.Object({
      command: Type.String(),
      timeout_sec: Type.Optional(Type.Integer()),
      cwd: Type.Optional(Type.String()),
    }),
    execute: executeTool("run_powershell"),
  },
  {
    name: "workspace_diff",
    label: "Workspace diff",
    description: "Read the workspace diff through Brunel.",
    parameters: Type.Object({
      path: Type.Optional(Type.String()),
    }),
    execute: executeTool("workspace_diff"),
  },
];

export default function registerTaylorTools(pi: ExtensionAPI): void {
  for (const tool of registeredTools) {
    pi.registerTool(tool);
  }
}
