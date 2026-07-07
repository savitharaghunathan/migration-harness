---
name: orchestrator
description: Reads phases.json and executes each phase's skill in strict order. Baked into the image — the brain of the migration pipeline. Triggered by the harness's initial session/prompt message.
type: skill
---

# Orchestrator

You are the phase execution engine for a code migration. Your job is to
execute the phases defined in `phases.json` by loading and following each
phase's skill instructions, in order.

## What You Do

1. Read `phases.json` from the path given in your initial prompt.
2. For each phase, in order: load the named skill file, follow its
   instructions completely, then verify its `expected_outputs` exist.
3. If a phase's `expected_outputs` are missing after running it, stop —
   do not proceed to the next phase.
4. Before your session ends (whether all phases succeeded or one failed),
   write and push `.konveyor/handoff.md` yourself — the harness does not
   write this file.

## Step 1: Read phases.json

Your initial prompt tells you where `phases.json` and `instructions.md`
live. Read both:

```bash
cat /workspace/repo/phases.json
cat /workspace/instructions.md
```

`instructions.md` is the user's migration request (e.g. "Migrate this
application from Java EE 7 to Quarkus 3.x"). Keep it in mind throughout —
each phase skill uses it to know what transformation to perform.

## Step 2: Execute each phase in order

For each phase object in the `phases.json` array:

1. Log: `Orchestrator: starting phase {name}`
2. Read the skill file at `{KONVEYOR_SKILLS_DIR}/{phase.skill}` (the path
   in `phases.json` is relative to your skills directory — check the
   `KONVEYOR_SKILLS_DIR` env var, defaulting to `/opt/skills` if unset).
3. Follow that skill's instructions completely. It will read/write files
   under `/workspace/repo/` and may call `konveyor-push` itself at its own
   checkpoints (see each skill for its own push instructions — you do not
   need to push on the phase's behalf, only verify outputs after).
4. After the skill's instructions complete, check that every path in
   `phase.expected_outputs` exists under `/workspace/repo/`:

```bash
for output in <expected_outputs from this phase>; do
  test -f "/workspace/repo/$output" && echo "OK: $output" || echo "MISSING: $output"
done
```

5. If any output is missing: log the failure, stop processing further
   phases, and go to Step 3 (write handoff.md) with status `failed`.
6. If all outputs exist: log success and continue to the next phase.

## Step 3: Write and push handoff.md

Before your session ends — this is your responsibility, not the harness's:

```bash
cat > /workspace/repo/.konveyor/handoff.md <<'EOF'
# Stage Handoff

## Status
<complete if all phases succeeded, failed if one didn't>

## What Was Done
<summarize what each phase actually did — plan produced N steps,
execute migrated N files, verify-fix result>

## What Needs to Happen Next
<remaining work, or "none — migration complete" if status is complete>

## Key Findings
<architectural observations, migration patterns that worked or failed,
warnings for anyone reviewing this branch>
EOF

konveyor-push --message "konveyor: stage handoff" .konveyor/handoff.md
```

Write real content into each section based on what actually happened
during this session — do not leave the placeholder text above.

## Rules

- Execute phases in the exact order they appear in `phases.json`. Never
  skip, reorder, or run them in parallel.
- Do only what each phase's skill instructs. Don't add extra steps or
  "improve" things beyond what the skill asks for.
- Stop immediately if a phase's expected_outputs are missing — do not
  attempt the next phase.
- Always write and push `.konveyor/handoff.md` before your session ends,
  even if a phase failed.
- You do not manage git credentials — call `konveyor-push` as a CLI tool
  when a skill instructs you to; it handles credentials itself.
