// Smoke test for the extension module graph: the near-AGENTS.md logic now
// lives in ./agents-md-delivery.ts, so the entry point must still load and
// register all eight tools (issue #47).
//
// Unlike agents-md-delivery.test.ts this touches typebox, which is a declared
// dependency but not vendored: run `npm ci` first. Without it the test skips
// rather than failing, so `npm test` stays usable on a bare checkout.
import assert from "node:assert/strict";
import test from "node:test";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const dependencyMissing = await import("typebox").then(
  () => undefined,
  (error: unknown) => (error instanceof Error ? error.message : String(error)),
);

test(
  "registers all eight tools with the shared AGENTS.md delivery module",
  { skip: dependencyMissing && `typebox not installed (run npm ci): ${dependencyMissing}` },
  async () => {
    const { default: registerTaylorTools } = await import("./taylor-tools.ts");
    const registered: { name?: string }[] = [];
    const pi = {
      registerTool(tool: { name?: string }) {
        registered.push(tool);
      },
    } as unknown as ExtensionAPI;

    registerTaylorTools(pi);

    assert.deepEqual(
      registered.map((tool) => tool.name),
      [
        "list_files",
        "search_text",
        "read_file",
        "apply_patch",
        "create_file",
        "write_file",
        "run_powershell",
        "workspace_diff",
      ],
    );
  },
);
