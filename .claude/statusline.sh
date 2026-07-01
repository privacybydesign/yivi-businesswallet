#!/usr/bin/env bash
# Shared Claude Code statusline for the plan/review + worktree workflow.
# Segments:  branch [package] | plan? | context% | model
# Reads session JSON on stdin, prints one line to stdout.
input=$(cat)
j() { printf '%s' "$input" | jq -r "$1 // empty" 2>/dev/null; }

G=$'\e[32m'; Y=$'\e[33m'; Rd=$'\e[31m'; C=$'\e[36m'; D=$'\e[2m'; X=$'\e[0m'

model=$(j '.model.display_name'); model=${model:-Claude}
cwd=$(j '.cwd'); [ -z "$cwd" ] && cwd=$(j '.workspace.current_dir'); [ -z "$cwd" ] && cwd=$PWD
ctx=$(j '.context_window.used_percentage')

# git branch + repo root, cached 2s (the statusline runs on every tick)
cache="/tmp/cc-sl-$(printf '%s' "$cwd" | cksum | cut -d' ' -f1)"
if [ -f "$cache" ] && [ "$(( $(date +%s) - $(stat -c %Y "$cache" 2>/dev/null || stat -f %m "$cache" 2>/dev/null) ))" -lt 2 ]; then
  IFS='|' read -r branch root < "$cache"
else
  branch=$(git -C "$cwd" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null)
  root=$(git -C "$cwd" --no-optional-locks rev-parse --show-toplevel 2>/dev/null)
  printf '%s|%s' "$branch" "$root" > "$cache"
fi
[ -z "$branch" ] && branch="no-git"

# which package in the monorepo
pkg=""
[ -n "$root" ] && case "$cwd" in
  "$root"/backend*)  pkg=" backend" ;;
  "$root"/frontend*) pkg=" frontend" ;;
esac

# plan present for this branch?
if [ -n "$root" ] && [ -f "$root/.ai/plans/$branch.md" ]; then
  plan="${G}plan${X}"
else
  plan="${Y}no plan${X}"
fi

# context, colored against a soft cap (early warning before compaction)
if [ -n "$ctx" ]; then
  pct=$(printf '%.0f' "$ctx")
  if   [ "$pct" -lt 55 ]; then col=$G
  elif [ "$pct" -lt 75 ]; then col=$Y
  else col=$Rd; fi
  ctxseg="${col}${pct}% ctx${X}"
else
  ctxseg="--% ctx"
fi

printf '%s' "${C}${branch}${pkg}${X} ${D}|${X} ${plan} ${D}|${X} ${ctxseg} ${D}|${X} ${D}${model}${X}"
