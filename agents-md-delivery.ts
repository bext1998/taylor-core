// Near-directory AGENTS.md delivery: how taylor-tools.ts attaches the rule
// that applies to the directory a tool call touched (issue #11) and how it
// keeps that delivery to once per rule per session (issue #47).
//
// Deliberately import-free: the decision logic stays unit-testable with
// `node --test` (Node strips the types) without installing dependencies.
// taylor-tools.ts owns the process-wide instance and does the actual I/O.

export type AgentsMDContext = {
  source: string;
  content: string;
};

// A rule is identified by the directory it applies to plus its exact text, so
// the same rule is delivered once per session while a rule edited mid-session
// is delivered again. Content is part of the key because the Go side keeps no
// cache and re-reads the file on every call: keying on the source alone would
// make a stale copy the model already holds permanent for the session.
export function agentsMDKey(md: AgentsMDContext): string {
  return `${md.source}\u0000${md.content}`;
}

export type AgentsMDSentTracker = {
  // True whenever the content for this source differs from what was last
  // delivered for it (including the first time). Records the delivery, so
  // callers must call it only when they are about to attach the rule to the
  // tool result.
  shouldSend(md: AgentsMDContext): boolean;
};

// One instance per Pi subprocess. Brunel launches `pi --mode rpc --no-session`
// per session (ADR-002), so a process-wide instance is session-wide.
//
// Tracks the last content delivered per source, not every content ever seen:
// a Set keyed by agentsMDKey would permanently suppress a rule whose content
// returns to an earlier value (edited A -> B -> A), leaving the model pinned
// to the stale B it was last shown even though the file is back to A.
export function createAgentsMDSentTracker(): AgentsMDSentTracker {
  const lastSent = new Map<string, string>();
  return {
    shouldSend(md: AgentsMDContext): boolean {
      if (lastSent.get(md.source) === md.content) {
        return false;
      }
      lastSent.set(md.source, md.content);
      return true;
    },
  };
}

export function agentsMDText(md: AgentsMDContext): string {
  return (
    `---Workspace agent instructions (AGENTS.md) — applies to: ${md.source}---\n` +
    md.content +
    `\n---end of AGENTS.md---`
  );
}
