---
name: verify
description: Runs the build/test commands from PLAN.md's Verification section. If they fail, fixes errors one at a time and re-verifies, up to 3 iterations. Writes verify-report.md with the final state.
type: skill
---

# Verify (includes Fix)

Verify the migrated codebase builds, and fix build errors if it doesn't —
up to 3 iterations. This is one phase, not two: don't treat verify and
fix as separate steps requiring separate phases.json entries.

## Phase 1 — Read the verification command

Read `/workspace/repo/PLAN.md`'s `## Verification` section — it has the
exact build/test command(s) to run (e.g. `mvn clean compile`,
`npm test`). Ignore the rest of `PLAN.md` for now.

## Phase 2 — Run verification

Run the command(s) from Phase 1. Capture:
- Build status (success/failure)
- Compilation/build errors (file, line, message) if it failed

## Phase 3 — Fix loop (only if Phase 2 failed)

Repeat up to 3 times:

1. Read `/workspace/repo/execution-log.md` for context on what the
   execute phase attempted for the file(s) with errors.
2. Pick the first remaining error. Make the minimal edit needed to fix
   it — only touch the file the error points at (or the build manifest,
   if it's a missing dependency). Do not refactor working code.
3. Re-run the verification command from Phase 1.
4. If it now passes, stop the loop. If it still fails, go to step 1 with
   the next error, up to 3 total iterations.

If still failing after 3 iterations, stop and report the remaining
errors — do not keep retrying beyond 3.

## Phase 4 — Write verify-report.md and push

```markdown
# Verify Report

## Build Status
<success | failure>

## Fix Iterations
<0-3>

## Fixes Applied
<description of what was fixed each iteration, or "none" if it passed
on the first try>

## Remaining Errors
<list, or "none">
```

```bash
konveyor-push --message "konveyor: verify-fix phase" verify-report.md
```

Also push any files changed during the fix loop, if not already pushed:

```bash
konveyor-push --message "konveyor: verify-fix phase" <files fixed during the loop>
```

## Rules

- Focus on build/compile errors first — test failures are acceptable to
  leave as remaining errors within the 3-iteration budget.
- Minimal, targeted fixes only. No refactoring.
- Maximum 3 fix iterations. Report and stop after that, even if errors
  remain.
