// Unit tests for the near-AGENTS.md delivery decision (issue #47).
//
// Run with `npm test` (node --test, Node strips the types). The module under
// test imports nothing, so these tests need no dependencies installed.
import assert from "node:assert/strict";
import test from "node:test";
import {
  agentsMDKey,
  agentsMDText,
  createAgentsMDSentTracker,
  type AgentsMDContext,
} from "./agents-md-delivery.ts";

function rule(source: string, content: string): AgentsMDContext {
  return { source, content };
}

test("sends the same rule once, then suppresses every later copy", () => {
  const tracker = createAgentsMDSentTracker();
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), true);
  // A distinct object with the same source and text is still the same rule.
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), false);
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), false);
});

test("sends once per directory, not once per content", () => {
  const tracker = createAgentsMDSentTracker();
  const shared = "no network access\n";
  assert.equal(tracker.shouldSend(rule("services/api", shared)), true);
  // The same text in another directory is a different rule: it carries a
  // different "applies to" source and must reach the model on its own.
  assert.equal(tracker.shouldSend(rule("services/web", shared)), true);
  assert.equal(tracker.shouldSend(rule("services/web", shared)), false);
});

test("re-sends a rule that changed mid-session instead of pinning a stale copy", () => {
  const tracker = createAgentsMDSentTracker();
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), true);
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\nrun lint\n")), true);
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\nrun lint\n")), false);
});

test("re-sends a rule that reverts to an earlier version, not just a new one", () => {
  const tracker = createAgentsMDSentTracker();
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), true);
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\nrun lint\n")), true);
  // Content reverted to the first version the model was ever shown: the
  // model's last known copy is still the second version, so this must
  // deliver again rather than being suppressed because that exact
  // source+content pair was already sent once before.
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), true);
  assert.equal(tracker.shouldSend(rule("services/api", "run gofmt\n")), false);
});

test("trackers do not share state, so a new session delivers again", () => {
  const first = createAgentsMDSentTracker();
  const second = createAgentsMDSentTracker();
  assert.equal(first.shouldSend(rule("services/api", "run gofmt\n")), true);
  assert.equal(second.shouldSend(rule("services/api", "run gofmt\n")), true);
});

test("a failed call does not consume the rule for that directory", () => {
  const tracker = createAgentsMDSentTracker();
  // executeTool's guard, as written in taylor-tools.ts. A failed tool call
  // carries no agents_md (cmd/brunel writes it only on success), so the
  // tracker is not reached and the rule stays pending: the next successful
  // call in that directory still delivers it.
  const attach = (response: { agents_md?: AgentsMDContext }): boolean =>
    response.agents_md !== undefined && tracker.shouldSend(response.agents_md);

  assert.equal(attach({}), false);
  assert.equal(attach({ agents_md: rule("services/api", "run gofmt\n") }), true);
  assert.equal(attach({ agents_md: rule("services/api", "run gofmt\n") }), false);
});

test("key separates source and content, and formatting carries the source", () => {
  const md = rule("services/api", "run gofmt\n");
  assert.notEqual(agentsMDKey(md), agentsMDKey(rule("services/web", "run gofmt\n")));
  assert.notEqual(agentsMDKey(md), agentsMDKey(rule("services/api", "other\n")));
  assert.equal(agentsMDKey(md), agentsMDKey(rule("services/api", "run gofmt\n")));

  assert.equal(
    agentsMDText(md),
    "---Workspace agent instructions (AGENTS.md) — applies to: services/api---\n" +
      "run gofmt\n" +
      "\n---end of AGENTS.md---",
  );
});
