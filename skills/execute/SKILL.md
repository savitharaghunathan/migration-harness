---
name: execute
description: Reads PLAN.md and executes each step in order, transforming one file at a time. Pushes after each file. Writes execution-log.md with lessons learned.
type: skill
---

# Execute

Execute the approved plan from `PLAN.md`, one step at a time, in the
order the steps appear.

## Startup

Read `/workspace/repo/PLAN.md` in full — the Goal, Project Summary, and
every Step. This is your only planning context; don't re-derive anything
already decided during the plan phase.

## Per-Step Execution Loop

For each step in `PLAN.md`, in order:

1. Read the current content of the step's `File` (if it exists — CREATE
   steps won't have existing content).
2. Apply the transformation described in "What to do".
3. Write/edit/delete the file per the step's `Action`.
4. Append a line to `/workspace/repo/execution-log.md` (create it on the
   first step):

```markdown
## Step N: <title>
- Status: ok | failed
- Files touched: <list>
- Lesson: <anything genuinely useful for the verify-fix phase, or omit>
```

5. Push this step's changes:

```bash
konveyor-push --message "konveyor: migrate <filename>" <files touched in this step>
```

6. Move to the next step.

## Rules

- Execute steps in the exact order `PLAN.md` lists them — a step's
  "Depends on" field means those steps must already be done.
- Do not compile, test, or verify — that's the next phase's job.
- Do not touch files outside the current step's scope.
- If a step is unclear or fails, log it in `execution-log.md` under that
  step with `Status: failed` and continue to the next step rather than
  stopping the whole phase — the verify-fix phase will surface remaining
  problems via the build.

## Completion

Once every step in `PLAN.md` has an entry in `execution-log.md`, do a
final push if anything is unpushed:

```bash
konveyor-push --message "konveyor: execute phase complete" execution-log.md
```

Report back: how many steps succeeded vs failed, and any lessons worth
flagging for the verify-fix phase.
