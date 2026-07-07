# Migration Harness — Agentic Platform Spec

Defines how the migration harness runs on the Konveyor Agentic Platform.
Aligns with the controller enhancement (PR #295) and informs the
agent-base-image-composition enhancement (PR #296).

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Container Image                     │
│                                                      │
│  ┌──────────────────────────────────────────────┐   │
│  │  Shell Entrypoint (thin)                      │   │
│  │  - Calls utilities, launches goose serve      │   │
│  └──────────────┬───────────────────────────────┘   │
│                 │ launches                            │
│                 ▼                                     │
│  ┌──────────────────────────────────────────────┐   │
│  │  Agent Runtime (goose serve)                  │   │
│  │                                               │   │
│  │  ┌──────────────────────────────────────┐    │   │
│  │  │  Orchestration Skill (THE BRAIN)      │    │   │
│  │  │                                       │    │   │
│  │  │  1. Detect  → calls graphify          │    │   │
│  │  │  2. Plan    → generates PLAN.md       │    │   │
│  │  │  3. Execute → migrates file by file   │    │   │
│  │  │  4. Verify  → runs build/tests        │    │   │
│  │  │  5. Fix     → iterates on errors      │    │   │
│  │  │                                       │    │   │
│  │  │  Calls utilities as needed            │    │   │
│  │  └──────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────┘   │
│                                                      │
│  Utilities on PATH (called by entrypoint OR skill):  │
│  ├── konveyor-clone     (git clone with creds)       │
│  ├── konveyor-push      (git commit + push)          │
│  ├── konveyor-configure (env vars → runtime config)  │
│  ├── konveyor-results   (write results.json)         │
│  ├── graphify           (code analysis)              │
│  ├── skillctl           (skill discovery)            │
│  └── git, jq, curl, python3                          │
│                                                      │
│  Skills:                                             │
│  ├── /opt/skills/orchestrator/   (baked in - brain)  │
│  ├── /opt/skills/javaee-quarkus/ (ImageVolume)       │
│  └── /opt/skills/python2-to-3/  (ImageVolume)        │
└─────────────────────────────────────────────────────┘
```

Three layers, not one monolithic binary:

| Layer | What | Where |
|---|---|---|
| Shell entrypoint | Plumbing — clone, launch runtime, cleanup | `/usr/local/bin/konveyor-entrypoint` |
| Orchestration skill | Brain — 5-phase migration pipeline | `/opt/skills/orchestrator/SKILL.md` (baked in) |
| Utilities | CLI tools called by entrypoint or skill | `/usr/local/bin/konveyor-*`, `graphify`, `skillctl` |

The "harness" is not a single program. It is the orchestration skill
(the brain) plus a thin entrypoint (plumbing) plus CLI utilities
(tools). PR #296 should not describe it as a "Go binary harness
entrypoint."

## Shell Entrypoint

The container's CMD. Handles infrastructure before and after the
agent runtime. Not the migration logic — that lives in the
orchestration skill.

```bash
#!/usr/bin/env bash
set -euo pipefail

# 1. Read KONVEYOR_* env vars
source_url="${KONVEYOR_PARAM_SOURCE_URL:?}"
target_branch="${KONVEYOR_PARAM_TARGET_BRANCH:?}"

# 2. Clone repo with credentials
konveyor-clone "$source_url" /workspace/repo

# 3. Checkout or create target branch
cd /workspace/repo
git checkout -B "$target_branch"

# 4. Configure agent runtime
konveyor-configure

# 5. Launch agent runtime
goose serve --port 4000 &
GOOSE_PID=$!

# 6. Wait for agent to finish
wait $GOOSE_PID
EXIT_CODE=$?

# 7. Final commit of session context
cd /workspace/repo
konveyor-push --message "konveyor: session context" \
  .konveyor/handoff.md .konveyor/session.json

# 8. Write pod-local results
konveyor-results --exit-code $EXIT_CODE
```

This is ~50 lines of shell. No Go code needed.

### What it does NOT do

- Migration logic (that's the orchestration skill)
- Decide when to push intermediate work (the skill calls `konveyor-push`)
- Interact with the Kubernetes API (the controller does that)
- Manage sessions or turns (the agent runtime does that)

## Orchestration Skill (The Brain)

The orchestration skill is a SKILL.md baked into the image at
`/opt/skills/orchestrator/SKILL.md`. It tells the agent runtime
how to run a migration. This is the core value of the harness.

### Five Phases

```
Phase 1: Detect
  - Run graphify against the codebase
  - Build code graph (AST, dependencies, communities)
  - Produce detect.json + graph.json
  - Zero LLM tokens — pure analysis

Phase 2: Plan
  - Read detect.json + graph.json for structural context
  - Read any loaded rule skills for migration patterns
  - Generate PLAN.md with ordered migration steps
  - Each step: action (MODIFY/CREATE/DELETE), file path, description
  - Call konveyor-push to save the plan

Phase 3: Execute
  - Read PLAN.md
  - For each step: apply the transformation
  - After each file: call konveyor-push to save progress
  - Produce execution-log.md with lessons learned per step

Phase 4: Verify
  - Read PLAN.md verification section for build commands
  - Run build/tests
  - If errors: attempt auto-fix (up to 3 iterations)
  - Read execution-log.md for context on what was attempted
  - Produce verify-report.md

Phase 5: Fix (conditional)
  - Only runs if Phase 4 left errors
  - Fix one error at a time, re-verify after each
  - Up to 3 iterations
  - Produce fix-report.md
```

### Language Agnosticism

The orchestration skill is language-agnostic. It knows the 5-phase
pipeline but does not contain language-specific migration knowledge.

Language-specific knowledge comes from **rule skills** mounted via
ImageVolumes:

| Rule skill | Mounted from | Provides |
|---|---|---|
| `javaee-quarkus` | SkillCard OCI artifact | Java EE → Quarkus patterns |
| `springboot-2-to-3` | SkillCard OCI artifact | Spring Boot upgrade patterns |
| `python2-to-python3` | SkillCard OCI artifact | Python 2 → 3 patterns |
| `dotnet-framework-to-core` | SkillCard OCI artifact | .NET migration patterns |

Rule skills are loaded into the agent's context as reference material.
The orchestration skill uses them during Plan and Execute phases
when it needs migration patterns. The orchestration skill does not
need to know which rules are loaded — the agent runtime discovers
them from `/opt/skills/`.

### Push Timing

The orchestration skill decides when to push, not the entrypoint.
Only the skill knows when a meaningful unit of work is complete.

```
After Plan phase:
  "Run konveyor-push to save PLAN.md"

After each Execute step:
  "Run konveyor-push to save progress on {file}"

After Verify/Fix:
  "Run konveyor-push to save verification results"
```

The agent calls `konveyor-push` as a CLI tool. The utility handles
git add, commit, and push with credentials. The agent does not
have direct access to push credentials.

## Utilities

CLI tools on PATH. Called by the entrypoint, the orchestration skill,
or both. Each utility does one thing.

### konveyor-clone

Clones a git repo with credentials into the workspace.

```
Usage: konveyor-clone <url> <dest>

- Reads git credentials from KONVEYOR_GIT_CREDENTIALS_PATH
  (mounted Secret)
- Clones the repo
- Strips credentials from the workspace remote
  (agent cannot push directly)
```

### konveyor-push

Commits and pushes specified files to the remote.

```
Usage: konveyor-push [--message <msg>] [files...]

- Stages specified files (or all changes if no files given)
- Commits with the given message
- Pushes using credentials from KONVEYOR_GIT_CREDENTIALS_PATH
- The agent calls this tool — it does not have direct push access
```

This is how push timing is controlled: the orchestration skill
tells the agent when to call `konveyor-push`. The utility handles
credentials. The agent never sees them.

### konveyor-configure

Translates KONVEYOR_* env vars into runtime-specific config files.

```
Usage: konveyor-configure

- Reads KONVEYOR_PARAM_* env vars
- Reads LLM credential env vars (from envFrom Secrets)
- Writes $HOME/.config/goose/config.yaml (for goose)
  or equivalent for other runtimes
- Creates $HOME/.config/<runtime>/ directory
```

### konveyor-results

Writes pod-local results for the controller to read.

```
Usage: konveyor-results --exit-code <N>

- Writes /.konveyor/results.json
- Read by the controller after pod completion
- This is pod-local (EmptyDir or container rootfs),
  NOT committed to git
```

### Existing utilities

| Utility | Purpose | Already exists |
|---|---|---|
| `graphify` | Code graph analysis (AST, tree-sitter) | Yes (pip package) |
| `skillctl` | Skill discovery, inspection, OCI operations | Yes (Go binary) |
| `git` | Version control | System package |
| `jq` | JSON processing | System package |
| `curl` | HTTP client | System package |

### Implementation

These utilities can be implemented in any language — shell scripts,
Go binaries, Python scripts. For the POC, shell scripts wrapping
`git` commands are sufficient. Go binaries can replace them later
if error handling or credential management needs to be more robust.

## Workspace and Filesystem

### Workspace: EmptyDir, NOT PVC

Per the controller enhancement (PR #295), workspaces are
**ephemeral** (EmptyDir). Git is the persistence layer. No PVCs
survive between runs.

The harness clones the repo, the agent works, the harness pushes.
Everything durable lives in git.

Cross-stage continuity in AgentPlaybooks happens via git: all
stages share the same target branch. Each stage clones the branch,
reads the previous stage's `.konveyor/handoff.md`, does its work,
commits, and pushes.

### Filesystem Layout

```
/workspace/                          EmptyDir mount
  repo/                              Cloned source repository
    .konveyor/                       Session context (committed to git)
      handoff.md                     Summary for next stage
      session.json                   Model, tokens, timing, tool calls
    PLAN.md                          Migration plan (committed)
    execution-log.md                 Execution progress (committed)
    verify-report.md                 Verification results (committed)
    fix-report.md                    Fix iteration results (committed)

/.konveyor/                          Pod-local (NOT committed to git)
  results.json                       Structured results for controller

/opt/skills/                         Skill mount point
  orchestrator/SKILL.md              Baked into image
  javaee-quarkus/SKILL.md            ImageVolume from SkillCard
  custom-rules/SKILL.md              ImageVolume from SkillCard

$HOME/.config/<runtime>/             Runtime config (written by konveyor-configure)
```

### Two `.konveyor/` Paths

There are two different `.konveyor/` locations with different
lifecycles:

| Path | Location | Lifecycle | Purpose |
|---|---|---|---|
| `/workspace/repo/.konveyor/` | Inside the cloned repo | Committed to git, survives pod death | Cross-stage handoff, audit trail |
| `/.konveyor/` | Pod-local (rootfs or EmptyDir) | Dies with the pod | Controller reads results after completion |

The entrypoint commits `/workspace/repo/.konveyor/handoff.md` and
`session.json` to git on exit. The controller reads
`/.konveyor/results.json` from the pod filesystem after completion.

## Session Handoff Schemas

The controller enhancement (PR #295) defers these schemas to this
spec. The harness commits these files to the target branch on exit
at `.konveyor/` within the repo.

### handoff.md

Written by the orchestration skill (or harness entrypoint on exit).
Read by the next stage's orchestration skill.

```markdown
# Stage Handoff: {stage_name}

## Status
{complete | failed}

## What Was Done
- {summary of work completed}
- {files changed, patterns applied}

## What Needs to Happen Next
- {remaining work for the next stage}
- {known issues to address}

## Key Findings
- {architectural observations}
- {migration patterns that worked or failed}
- {warnings for the next stage}
```

### session.json

Written by the harness entrypoint on exit. Contains machine-readable
session metadata for audit and observability.

```json
{
  "session_id": "uuid",
  "stage": "execution",
  "status": "complete",
  "started_at": "2026-07-01T10:00:00Z",
  "completed_at": "2026-07-01T10:45:00Z",
  "duration_seconds": 2700,
  "runtime": "goose",
  "model": {
    "provider": "anthropic",
    "name": "claude-sonnet-4-20250514",
    "role": "primary"
  },
  "token_usage": {
    "input_tokens": 125000,
    "output_tokens": 45000
  },
  "phases_completed": ["detect", "plan", "execute", "verify"],
  "phases_failed": [],
  "git": {
    "source_url": "https://github.com/acme/legacy-app.git",
    "target_branch": "konveyor/migrate-app-123",
    "commits": 12,
    "last_commit_sha": "abc1234"
  },
  "artifacts": [
    "detect.json",
    "graph.json",
    "PLAN.md",
    "execution-log.md",
    "verify-report.md"
  ]
}
```

### results.json (pod-local)

Written by `konveyor-results` to `/.konveyor/results.json`.
Read by the controller after pod completion to update AgentRun
CR status. Not committed to git — dies with the pod.

```json
{
  "status": "succeeded",
  "exit_code": 0,
  "duration_seconds": 2700,
  "git": {
    "branch": "konveyor/migrate-app-123",
    "last_commit_sha": "abc1234"
  }
}
```

The controller may simplify or remove this file if it reads status
via ACP instead (per PR #295 observability design). Keep it minimal.

## Mapping to Agentic Platform CRDs

How the harness maps onto the CRDs defined in PR #295.

### SkillCards

| SkillCard | Type | Source | Baked in or mounted |
|---|---|---|---|
| `orchestrator` | skill | This repo | Baked into image |
| `detect` | skill | This repo (future: separate) | Baked into image |
| `plan` | skill | This repo (future: separate) | Baked into image |
| `execute` | skill | This repo (future: separate) | Baked into image |
| `verify` | skill | This repo (future: separate) | Baked into image |
| `fix` | skill | This repo (future: separate) | Baked into image |
| `javaee-quarkus` | rule | Separate repo/OCI | ImageVolume |
| `springboot-2-to-3` | rule | Separate repo/OCI | ImageVolume |
| `python2-to-python3` | rule | Separate repo/OCI | ImageVolume |
| `dotnet-framework-to-core` | rule | Separate repo/OCI | ImageVolume |

The orchestration skill and phase skills are baked into the image
because they are tightly coupled to the harness pipeline. Language-
specific rule skills are mounted via ImageVolumes because they are
independent and have separate release cadences.

### Agent CR

```yaml
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: java-migration-agent
spec:
  image: quay.io/konveyor/agent-java-goose:v1.0.0
  prompt: |
    You are a migration agent. Follow the orchestrator skill
    to execute the migration pipeline.
  providers:
    - ref: anthropic-provider
  skillCards:
    - ref: javaee-quarkus
  params:
    - name: source_url
      type: string
      description: Git URL of the application source
    - name: target_branch
      type: string
      description: Branch to push results to
```

The orchestrator skill is baked into the image, not referenced
as a SkillCard on the Agent CR. Only externally-mounted skills
appear as SkillCard refs.

### AgentRun CR

```yaml
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata:
  name: migrate-app-123
spec:
  agentRef: java-migration-agent
  models:
    - role: primary
      provider: anthropic-provider
      model: claude-sonnet-4-20250514
  params:
    - name: source_url
      value: https://github.com/acme/legacy-app.git
    - name: target_branch
      value: konveyor/migrate-app-123
  instructions: |
    Migrate this application from Java EE 7 to Quarkus 3.x.
  envFrom:
    - secretRef:
        name: git-write-creds
```

### AgentPlaybook (multi-stage)

For migrations that need separate stages with different models
or resource limits:

```yaml
apiVersion: konveyor.io/v1alpha1
kind: AgentPlaybook
metadata:
  name: java-migration
spec:
  guide: |
    Migrate a Java EE application to Quarkus.
  stages:
    - name: analysis
      agentRef: planning-agent
      instructions: Run detect and plan phases only.
    - name: implementation
      agentRef: execution-agent
      instructions: Read PLAN.md. Execute migration.
    - name: verification
      agentRef: verification-agent
      instructions: Verify build. Fix errors.
```

Each stage gets a separate Sandbox (EmptyDir workspace). The
controller clones the target branch (which has the previous
stage's commits) into each new Sandbox. Cross-stage handoff
happens via `.konveyor/handoff.md` committed to git.

## Git Lifecycle

Per PR #295, the harness manages git credentials. The agent
does not receive credentials directly.

```
1. Entrypoint reads git credentials from mounted Secret
2. konveyor-clone: clone source repo into /workspace/repo
3. Entrypoint: strip credentials from workspace remote
   (agent cannot git push directly)
4. Entrypoint: create or checkout target branch
5. Agent works, orchestration skill calls konveyor-push
   at meaningful checkpoints
6. On exit: entrypoint commits .konveyor/handoff.md
   and .konveyor/session.json
7. Entrypoint: final push to target repo (using credentials)
8. konveyor-results: write /.konveyor/results.json (pod-local)
```

**Credential isolation**: The agent cannot push directly. The
`konveyor-push` utility reads credentials from
`KONVEYOR_GIT_CREDENTIALS_PATH` (a mounted Secret path). The
agent calls `konveyor-push` as a CLI tool — it does not see or
handle credentials. In the POC (single-container Sandbox), the
credential exists on the container filesystem. This reduces
accidental exposure but does not provide full isolation. Full
isolation requires OpenShell filesystem policy enforcement (future).

## Agent Runtime Launch

For the POC, the entrypoint launches `goose serve --port 4000`
which exposes the full ACP (Agent Client Protocol) over HTTP/SSE
and WebSocket. The controller connects to this endpoint for
observability. The UI connects (via Hub proxy) for human-in-the-loop
interaction.

For non-Goose runtimes (OpenCode, etc.), the entrypoint launches
the runtime over stdio and bridges it to HTTP — same external
interface regardless of runtime.

The entrypoint auto-detects which runtime is present by checking
PATH for known binaries (`goose`, `opencode`).

## Image Composition

This spec informs PR #296. Key corrections to that enhancement:

### What changes in PR #296

1. **No "Go binary harness"** — The entrypoint is a shell script,
   not a Go binary. Utilities may be Go, shell, or any language.

2. **Orchestration skill baked into image** — The base image
   ships with the orchestration skill at `/opt/skills/orchestrator/`.
   This is the brain. PR #296's Summary saying "Skills are NOT
   baked into the image" is incorrect.

3. **EmptyDir, not PVC** — Workspace is ephemeral. Git is the
   persistence layer. PR #296's references to PVC must be updated.

4. **Two .konveyor/ paths** — Pod-local `/.konveyor/results.json`
   and git-committed `/workspace/repo/.konveyor/` are different.
   PR #296 conflates them.

5. **Session handoff schemas** — PR #295 defers `handoff.md` and
   `session.json` schema to the image enhancement. Defined in
   this spec above.

6. **Utility binaries, not a harness binary** — The image contents
   table should list individual utilities (`konveyor-clone`,
   `konveyor-push`, `konveyor-configure`, `konveyor-results`)
   instead of a single `konveyor-harness` Go binary.

### Image contents (corrected)

| Path | Component | Purpose |
|---|---|---|
| `/usr/local/bin/konveyor-entrypoint` | Shell script | Container CMD — clone, launch runtime, cleanup |
| `/usr/local/bin/konveyor-clone` | Utility | Git clone with credential handling |
| `/usr/local/bin/konveyor-push` | Utility | Git commit + push with credentials |
| `/usr/local/bin/konveyor-configure` | Utility | Env vars → runtime config |
| `/usr/local/bin/konveyor-results` | Utility | Write pod-local results.json |
| `/usr/local/bin/skillctl` | skillimage CLI | Skill discovery and inspection |
| `/usr/local/bin/graphify` | graphify CLI | Code graph analysis (tree-sitter) |
| `/usr/bin/git` | git | Version control |
| `/usr/bin/jq` | jq | JSON processing |
| `/usr/bin/curl` | curl | HTTP client |
| `/usr/bin/python3` | Python 3.12 | Required by graphify |
| `/opt/skills/orchestrator/SKILL.md` | Orchestration skill | The migration pipeline brain |

## What Lives Where

| Component | Repo | Why |
|---|---|---|
| Shell entrypoint | `konveyor/migration-harness` | Tightly coupled to pipeline |
| Orchestration skill | `konveyor/migration-harness` | IS the pipeline |
| Phase skills (detect, plan, etc.) | `konveyor/migration-harness` | Coupled to orchestrator |
| Utility scripts/binaries | `konveyor/migration-harness` | Support the pipeline |
| Language rule skills | Separate repos/OCI | Independent release cadence |
| Dockerfiles | `konveyor/migration-harness` | Build the images |
| Controller | `konveyor/agentic-controller` | Separate concern |

## Open Questions

1. **Orchestration: meta-skill or entrypoint?** The current
   `meta-skill/orchestrate.md` reads `phases.json` and sequences
   skills. An alternative is the flat model where the orchestration
   skill itself contains the 5-phase pipeline inline. The meta-skill
   approach is more flexible (controller defines phases), the inline
   approach is simpler (skill knows what to do). Which model for POC?

2. **Utility implementation language**: Shell scripts for POC?
   Go binaries later if credential handling needs hardening?

3. **graphify installation**: Currently pip-installed (requires
   Python 3.12 + dev headers in image). A future rewrite in
   another language could remove the Python dependency from
   agent-base, but this is not on the POC roadmap.

4. **Which phase skills to bake in**: Should all 5 phase skills
   (detect, plan, execute, verify, fix) be baked into the image,
   or should only the orchestrator be baked in with phase skills
   mounted via ImageVolumes? Baking them in is simpler and they're
   tightly coupled to the orchestrator anyway.
