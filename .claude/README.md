# Claude Code adapter

Tool-specific wiring for Claude Code only. No project knowledge lives here.
Knowledge stays universal in AGENTS.md and .ai/; these files only point at it.

- agents/plan-reviewer.md  subagent that runs the protocol in .ai/plans/README.md
- skills/review/SKILL.md    the /review command that dispatches it
- settings.json             permissions scoped to the AGENTS.md verify sequence

Adding another tool (Codex, Cursor)? Give it its own thin adapter pointing at the
same .ai/ files. Never copy a convention into an adapter, it will drift.
