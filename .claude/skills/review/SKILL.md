---
name: review
description: Review the current diff against this branch's plan
disable-model-invocation: true
---
The plan is .ai/plans/<branch>.md, where <branch> is `git branch --show-current`
(or $1 if an argument is given). Use the plan-reviewer subagent to review the
current diff against it, following .ai/plans/README.md.
