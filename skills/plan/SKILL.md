---
name: plan
description: Reads detect.json and graph.json, selects a matching migration rule skill if one is mounted, and writes PLAN.md with a specific, ordered migration plan for this project. Does not modify any source files.
type: skill
---

# Plan

Read the project's structure, pick the right migration pattern, and write
`PLAN.md`. This phase does not touch source files — planning only.

## Phase 1 — Read what's already known

`detect.json` and `graph.json` were produced by the harness's detect step
(no LLM was involved) and already exist at the repo root:

```bash
cat /workspace/repo/detect.json
```

`graph.json` is the full code graph — read it selectively (it can be
large), focusing on `nodes`, `communities`, and any node with
`degree > 20` (flagged as `god_nodes` in `detect.json` — these are
high-risk, central files).

## Phase 2 — Select a migration reference, if one is mounted

Check `{KONVEYOR_SKILLS_DIR}/` (default `/opt/skills`) for a rule skill
matching this project's stack — for example `javaee-quarkus/SKILL.md` for
a Maven project with `javax.ejb`/`javax.jms` usage, or
`python2-to-python3/SKILL.md` for a Python 2 codebase. Match based on
`detect.json`'s `manifests` block:

- `pom_xml: true` → check for a Java rule skill
- `requirements_txt` or `setup_py: true` → check for a Python rule skill

If a matching rule skill is mounted, read it — it contains the migration
order, transformation patterns, and files to delete/create. If none
matches, proceed with generic judgment based on `instructions.md` and the
graph structure alone.

## Phase 3 — Read a few source files, selectively

Read the build manifest (`pom.xml`, `package.json`, etc. — always).
Beyond that, only read files the graph flags as complex (god nodes, or
files matching a rule skill's "complex pattern" list). Aim for 5-10 total
file reads across this phase — the graph and rule skill should cover
everything else.

## Phase 4 — Write PLAN.md

Write `/workspace/repo/PLAN.md` with this structure:

```markdown
# PLAN.md

## Goal
<restate the migration goal from instructions.md in one sentence>
- Reference used: <rule skill name, or "none">

## Project Summary
- Type: <Maven/Node/Python/etc, from detect.json manifests>
- Files affected: <N>
- Estimated complexity: <Low/Medium/High>

## Steps

### Step 1: <title>
- File: <exact path from repo root>
- Action: <CREATE | MODIFY | DELETE>
- What to do: <specific instructions>
- Depends on: <step numbers, or "none">

### Step 2: <title>
...

## Verification
<exact build/test command(s) to run, e.g. `mvn clean compile`>

## Notes
<gotchas, decisions made>
```

Rules for steps: one file per step, exact paths (not placeholders),
dependency order (steps others depend on come first), build config before
source before tests before deletions.

## Phase 5 — Push and finish

```bash
konveyor-push --message "konveyor: plan phase" PLAN.md
```

Report back: how many steps, which reference (if any) you used, and a
one-line summary of the plan.
