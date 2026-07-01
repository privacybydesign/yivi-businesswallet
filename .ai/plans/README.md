# Planning & Review

Tool-agnostic. Any coding agent (Claude Code, Codex, Cursor) follows this.

Plans here are scratch. The whole directory is gitignored except this README.
A plan lives only as long as its branch; nothing here is a source of truth.
Durable knowledge is extracted on merge (see Harvest) into `.ai/conventions/`
or `.ai/features/`, which are committed.

## Plans
- One plan per branch: `.ai/plans/<branch-name>.md`. Never share a plan file across worktrees.
- A plan states: the goal, the files and interfaces touched, what is out of scope,
  a "Done when" section, and a "Harvest" section.
- "Done when" is a checklist of concrete pass/fail checks a reviewer can verify, not prose:
  - Bad:  "Add org invitations."
  - Good: "Done when: invite handler audits through the store in-tx; expired-invite path
           has an integration test; the AGENTS.md backend verify sequence passes."

## Harvest (complete before merge)
The plan is scratch, so anything durable it produced must move to a committed home
before the branch merges. Force the decision, never leave it implicit:
- Convention to add or update in `.ai/conventions/<area>.md`?  -> path, or "none"
- Feature doc to write or update in `.ai/features/<name>.md`?   -> path, or "none"

## Review
When a branch looks done, a fresh reviewer checks the diff against its plan:
1. Every requirement in the plan is implemented.
2. Every "Done when" check has a test, and the AGENTS.md verify sequence passes.
3. Nothing outside the plan's scope changed.
4. The Harvest section is filled in, and any convention or feature doc it names
   actually landed in the diff, not just promised.

Report only gaps that affect correctness or a stated requirement. Not style, not
speculative hardening, not cases the plan did not call for. A reviewer told to find
gaps will invent them, so scope it to correctness or it drives over-engineering.
