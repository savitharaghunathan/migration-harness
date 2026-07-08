# Migration Harness Restructure — Design Spec

Restructure the migration-harness from a bash CLI into Go binaries + SKILL.md
files that run on the Konveyor Agentic Platform. Aligns with the controller
enhancement (PR #295) and agent-base-image-composition enhancement (PR #296).

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Container Image                        │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │  konveyor-harness (Go binary, entrypoint)          │  │
│  │                                                    │  │
│  │  1. Read KONVEYOR_PARAM_* env vars                 │  │
│  │  2. konveyor-clone (clone repo with creds)         │  │
│  │  3. konveyor-configure (env → runtime config)      │  │
│  │  4. konveyor-detect (graphify, no LLM)             │  │
│  │  5. konveyor-push (push detect artifacts)          │  │
│  │  6. Write instructions.md, generate phases.json    │  │
│  │  7. Launch goose serve, dial ws://.../acp,          │  │
│  │     JSON-RPC: initialize → session/new →            │  │
│  │     session/prompt (BLOCKS until turn completes)    │  │
│  │  8. Response carries stopReason+usage directly;     │  │
│  │     check phases.json outputs on disk; SIGTERM      │  │
│  │     goose serve and wait for it to stop             │  │
│  │  9. konveyor-push (session.json; skill already     │  │
│  │     pushed handoff.md before session ended)        │  │
│  │ 10. konveyor-results (pod-local results)           │  │
│  └──────────────┬─────────────────────────────────────┘  │
│                 │ launches                                │
│                 ▼                                         │
│  ┌────────────────────────────────────────────────────┐  │
│  │  goose serve (agent runtime, ACP over WebSocket)    │  │
│  │  Harness drives the session over WebSocket JSON-RPC │  │
│  │  (initialize, session/new, session/prompt) — a      │  │
│  │  controller can attach a read-only SSE observer to  │  │
│  │  the same connection via Acp-Connection-Id          │  │
│  │                                                    │  │
│  │  Meta-skill reads phases.json, sequences:          │  │
│  │    Phase 1: plan       → loads plan SKILL.md        │  │
│  │    Phase 2: execute    → loads execute SKILL.md     │  │
│  │    Phase 3: verify-fix → loads verify SKILL.md      │  │
│  │      (verify runs build, fix errors, repeat max 3x) │  │
│  │                                                    │  │
│  │  Each phase can call konveyor-push mid-work         │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  Go utilities on PATH:                                   │
│  ├── konveyor-clone      (git clone with creds)          │
│  ├── konveyor-push       (git add/commit/push with creds)│
│  ├── konveyor-configure  (env vars → runtime config)     │
│  ├── konveyor-results    (write pod-local results.json)  │
│  └── konveyor-detect     (run graphify, write outputs)   │
│                                                          │
│  Skills (configurable path, /opt/skills/ in container):  │
│  ├── orchestrator/SKILL.md  (meta-skill, baked in)       │
│  ├── plan/SKILL.md          (baked in)                   │
│  ├── execute/SKILL.md       (baked in)                   │
│  ├── verify/SKILL.md        (baked in, includes fix loop) │
│  ├── javaee-quarkus/        (ImageVolume, rule)           │
│  └── python2-to-python3/    (ImageVolume, rule)           │
└─────────────────────────────────────────────────────────┘
```

### Key Decisions

- **Detect is Go, not a goose skill** — deterministic, no LLM tokens.
- **Harness generates phases.json** — it owns the pipeline, not the controller.
  The controller is domain-agnostic; the harness has migration knowledge.
- **Harness pushes around the goose session** — after detect (before any
  LLM work starts) and on exit. This guarantees detect output and
  session.json survive a crash; it does NOT guarantee plan/execute/
  verify-fix work survives a crash — those pushes are skill-driven
  (see below), so durability there depends on the orchestration skill
  actually calling `konveyor-push` before a crash.
- **Skills push mid-phase** — via `konveyor-push` as a CLI tool on PATH,
  called by the orchestration skill after PLAN.md, after each migrated
  file, and after the verify-fix loop. This is where most durability
  actually comes from, and it depends on the skill, not the harness.
- **goose serve + ACP drives the session, not `goose run`** — the harness
  launches `goose serve` for both driving work and exposing observability
  on the same endpoint. See "Launching and Driving Goose" below.
- **Pipeline skills baked into image (POC deviation from PR #296)** —
  tightly coupled to harness, simpler for POC. PR #296 states skills are
  never baked in, only mounted via ImageVolumes. This is a known, intentional
  deviation to revisit with the PR #296 authors — see Open Questions.
- **Language rule skills mounted via ImageVolume** — independent release cadence,
  matches PR #296.
- **Skills path is configurable** — `KONVEYOR_SKILLS_DIR` env var, defaults
  to `/opt/skills/` in container, `./skills/` for local dev.

## Go Binaries

All binaries live in one Go module, sharing `internal/` for git operations
and credential handling.

### konveyor-harness

The entrypoint. Container CMD on runtime images (agent-base-goose), NOT
on agent-base (which has no runtime).

```
konveyor-harness run
  1. Read env: KONVEYOR_PARAM_SOURCE_URL, KONVEYOR_PARAM_TARGET_BRANCH,
     KONVEYOR_INSTRUCTIONS, etc.
  2. Call konveyor-clone
  3. Call konveyor-configure
  4. Call konveyor-detect (graphify → detect.json, graph.json)
  5. Call konveyor-push (detect artifacts)
  6. Write instructions.md, generate phases.json from pipeline config
  7. Launch goose serve --port 4000 (background), open session,
     subscribe to its event stream, send session/prompt (see
     "Launching and Driving Goose" below)
  8. Consume the event stream until a terminal event; accumulate token
     usage from it; check phases.json expected_outputs on disk for
     step completion; SIGTERM goose serve and wait for it to stop
  9. Call konveyor-push (session.json only — the orchestration skill
     already pushed handoff.md before the session ended)
  10. Call konveyor-results

konveyor-harness version
  Print version info.
```

### Launching and Driving Goose

**CONFIRMED against a real `goose serve` instance** (see
`docs/superpowers/plans/acp-verification-runbook.md` for the exact
procedure used) — this section previously documented an inferred,
unverified design; every claim below has been empirically verified.

`goose run` and `goose serve` start independent sessions — a `goose run`
process would not share state with a `goose serve` instance already
listening on port 4000. Since PR #295 requires `goose serve` for ACP
observability (controller connects to `/acp`), the harness also uses
`goose serve` to drive the actual work, rather than shelling out to a
separate `goose run` invocation.

**The real transport is WebSocket, not plain HTTP POST/GET, and not
SSE for driving.** `goose serve`'s `/acp` endpoint requires a WebSocket
upgrade handshake; the server returns an `Acp-Connection-Id` response
header on the `101 Switching Protocols` response. All driving happens
as JSON-RPC 2.0 messages over that single WebSocket connection —
requests/responses and server-initiated notifications are multiplexed
on the same socket. A bare HTTP GET or POST to `/acp` (no WebSocket
upgrade) returns 4xx errors demanding either an `Accept:
text/event-stream` header or an already-established
`Acp-Connection-Id` — this is the read-only *observer* path (see
"Controller Observability" below), not how the harness itself drives.

**`session/prompt` blocks until the turn completes — it is not
fire-and-forget.** This was the single biggest wrong assumption in the
prior design. The JSON-RPC response to `session/prompt` only arrives
once the agent's full turn finishes, and it carries `stopReason` and
`usage` (`totalTokens`/`inputTokens`/`outputTokens`) directly:

```json
{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"totalTokens":11286,"inputTokens":11238,"outputTokens":48}},"id":3}
```

This means **no separate event-stream-consumption loop is needed to
detect completion or collect token usage** — awaiting the blocking RPC
call is sufficient. `session/update` notifications (message chunks,
thought chunks, live usage snapshots) still arrive on the same
WebSocket during the turn, but consuming them is optional — useful only
for live progress logging, not required for correctness.

**Confirmed real protocol sequence:**

```
1. Launch: GOOSE_SERVER__SECRET_KEY=<generated or controller-supplied> goose serve --port 4000 &
2. Poll http://localhost:4000/acp with a bare GET until it responds at
   all (connection accepted) — confirms the process is listening.
   Any response (even a 4xx) means it's up; connection-refused means not yet.
3. Dial ws://localhost:4000/acp (WebSocket upgrade).
   Capture Acp-Connection-Id from the upgrade response header — this is
   what a controller would need to attach an observer (see below).
4. JSON-RPC over the WebSocket:
   → {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1.0","clientInfo":{...}}}
   ← {"jsonrpc":"2.0","result":{"agentCapabilities":{...},"authMethods":[...]},"id":1}
5. → {"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/workspace/repo","mcpServers":[]}}
   ← {"jsonrpc":"2.0","result":{"sessionId":"20260708_2","modes":{...},"models":{...}},"id":2}
   (sessionId is a date-based string, e.g. "20260708_2" — not a UUID)
6. → {"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"20260708_2","prompt":[{"type":"text","text":"Load skill $KONVEYOR_SKILLS_DIR/orchestrator/SKILL.md. Instructions: <contents of instructions.md>. phases.json is at /workspace/repo/phases.json."}]}}
   (this call BLOCKS — does not return until the full turn completes)
   ... session/update notifications may arrive on the same socket while waiting, optional to consume ...
   ← {"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"totalTokens":N,"inputTokens":N,"outputTokens":N}},"id":3}
7. Read /workspace/repo/ on disk to determine which of phases.json's
   expected_outputs actually exist — this is how steps_completed /
   steps_failed in session.json is built, since session/prompt's
   response only reports overall turn status, not per-phase status
8. Send SIGTERM to the backgrounded goose serve process and wait for it
   to stop (bounded timeout) — the container's lifetime is the
   harness's lifetime, so goose serve must be stopped explicitly, not
   left to be killed by the container runtime tearing down the pod
```

**Only `"end_turn"` is treated as success.** Other possible
`stopReason` values (e.g. hitting a turn limit, an error, a refusal)
were not observed during verification — treat anything other than
`"end_turn"` as a failure for now (fail-closed), and expand this list
as real failure modes are observed in practice.

**WebSocket client library**: Go's standard library has no WebSocket
client. `internal/acp` uses `github.com/coder/websocket` (ISC license,
OSS) — the one exception to this project's stdlib-only rule, adopted
because implementing RFC 6455 framing by hand (masking, ping/pong,
close handshake) is not a good use of effort for a POC. Chosen over
`gorilla/websocket` for its `context.Context`-native API, which matches
this codebase's existing cancellation patterns.

### Controller Observability (SSE) — Confirmed, Narrower Than Assumed

A controller can attach a **read-only observer** to the harness's
active session: `GET /acp` with `Accept: text/event-stream` and the
`Acp-Connection-Id` header set to the value the harness's own WebSocket
handshake received. This does open a real SSE stream (confirmed, status
200, `content-type: text/event-stream`).

**However, this SSE stream only relays completed request/response
pairs — not live `session/update` notifications.** Verified twice,
including with raw unbuffered reads and explicit timestamps, ruling out
a buffering artifact: the SSE observer saw the `initialize` result, the
`session/new` result, and the final `session/prompt` result (each
exactly matching what arrived on the driving WebSocket), but received
**zero** of the interleaved `session/update` notifications (message
chunks, thought chunks, live usage snapshots) that arrived on the
WebSocket during the same window. A controller watching via SSE sees
"session created" and "turn completed, here's the token count," not
live token-by-token progress.

This satisfies PR #295's stated need (controller writes AgentRun CR
status at lifecycle transitions only — started/completed/failed, not
live streaming) — see "Observability Ownership" below — but is a real,
confirmed limitation if fuller live observability is ever wanted.

**Still unresolved**: how the controller obtains the `Acp-Connection-Id`
in the first place, since it's minted by the harness's own WebSocket
handshake and isn't otherwise exposed anywhere the controller can read
it yet (not written to a file, not in an env var). This is a real gap
to close when the controller side is actually built.

**Known limitation**: agent-invoked `konveyor-push` calls (used throughout
`skills/plan`, `skills/execute`, `skills/verify`, `skills/orchestrator`)
currently fail. `launchGoose` strips `KONVEYOR_GIT_USERNAME`/`KONVEYOR_GIT_TOKEN`
from the agent's environment (required — the agent must never hold git push
credentials), but `konveyor-push` needs those same env vars to authenticate,
so every agent-triggered push errors out. Two fixes were scoped and
discussed: (a) a local push-broker — the harness starts a Unix domain socket
before launching the agent; `konveyor-push` talks to the broker instead of
reading env vars directly; the harness (which still holds real credentials)
performs the actual push. Zero SKILL.md changes needed. (b) a harness-side
background auto-committer — remove the `konveyor-push` calls from all skill
files; the harness periodically commits/pushes any working-tree changes on
a timer using credentials it already has. Trade-off: (a) preserves
per-step commit messages but adds an IPC mechanism; (b) is structurally
simpler but loses per-step commit granularity (generic timer-driven commit
messages instead of "konveyor: migrate \<file\>"). Deferred for POC; (a)
is the recommended direction when this is picked up.

### Observability Ownership (Controller reads ACP, not files)

Per the controller enhancement, the controller connects to the agent's
ACP endpoint directly (as described above) for status, and writes
AgentRun CR status only at lifecycle transitions (started, completed,
failed) — not by polling harness-written files. Concretely:

- The harness does **not** update any Kubernetes CR itself (no SA
  token, no Kubernetes API calls from inside the container) — the
  controller owns that, driven by its own ACP connection.
- The harness's `.konveyor/session.json` (committed to git) is a
  **post-run audit trail**, not the live observability channel — the
  controller's live view comes from ACP, not from this file.
- `/.konveyor/results.json` (pod-local) is retained as a **fallback**
  for cases where a controller isn't attached via ACP (e.g. standalone
  CLI-style runs without a controller) — not the controller's primary
  status source, but still written by the harness so pod-local
  tooling has something to read after the container exits.

### Context Budget: Out of Scope for the POC

Context budget validation (counting rule/skill token sizes against a
model's context window before a run starts) is explicitly deferred by
the controller enhancement. The harness has no logic related to this
and should not gain any — it is not this project's responsibility for
the POC.

### konveyor-clone

Clone a git repo with credentials, strip them from the workspace.

```
konveyor-clone <url> <dest>
  - Read creds from env vars injected via envFrom/secretRef
    (e.g. KONVEYOR_GIT_USERNAME, KONVEYOR_GIT_TOKEN — matches PR #295's
    AgentRun envFrom model, NOT a mounted Secret file path)
  - git clone <url> <dest>
  - Strip creds from workspace remote (agent can't push directly)
  - Checkout or create target branch from KONVEYOR_PARAM_TARGET_BRANCH
```

### konveyor-push

Stage, commit, push. Called by harness AND by goose skills.

```
konveyor-push [--message <msg>] [files...]
  - Read creds from the same env vars as konveyor-clone (KONVEYOR_GIT_USERNAME,
    KONVEYOR_GIT_TOKEN)
  - git add <files> (or all changes if no files specified)
  - git commit -m <msg>
  - Push using GIT_ASKPASS pointed at a small in-image helper script that
    echoes the token from env — konveyor-clone stripped creds from the
    remote URL itself, so push-time auth goes through GIT_ASKPASS rather
    than a persistently-credentialed remote
  - Exit 0 on success, non-zero on failure
```

### konveyor-configure

Translate env vars to runtime-specific config files.

```
konveyor-configure
  - Read KONVEYOR_PARAM_* env vars
  - Read LLM credential env vars (from envFrom Secrets)
  - Detect runtime (goose, opencode) by checking PATH
  - Write $HOME/.config/goose/config.yaml (or equivalent)
  - Determine the GOOSE_SERVER__SECRET_KEY for this run and export it
    (goose serve requires this for auth): use KONVEYOR_GOOSE_SECRET_KEY
    if a controller has supplied one via env, otherwise generate one
    randomly as a local fallback for standalone runs without a
    controller. The controller-supplied path exists so a future
    controller/UI can authenticate to this run's /acp endpoint for
    observability, but the controller side of generating and injecting
    that value is not yet designed/implemented (separate repo)
```

### konveyor-detect

Run graphify, write structured outputs. No LLM.

```
konveyor-detect <repo-path>
  - Run graphify against <repo-path> (subprocess)
  - Parse output → detect.json (manifests, file counts, graph stats)
  - Copy graph.json to workspace
  - Zero LLM tokens
```

### konveyor-results

Write pod-local results for the controller.

```
konveyor-results --exit-code <N> [--target-branch <branch>] [--commits <N>] [--last-commit-sha <sha>]
  - Write /.konveyor/results.json
  - Contains: status, exit_code, duration, git branch, last commit SHA
  - --target-branch/--commits/--last-commit-sha are optional; if
    omitted, the Git fields default to their zero value (empty
    string / 0). The harness itself doesn't invoke this binary as a
    subprocess — it builds session.Results in-process with full git
    info already available — these flags exist for standalone/manual
    invocation.
  - Pod-local only — NOT committed to git
  - Written for a pod-local fallback use case (e.g. tooling that
    inspects the running/recently-exited container directly), NOT a
    reliable channel for the controller — the controller reads status
    via the ACP connection instead (see "Observability Ownership"
    below). Since /.konveyor/ is on the container's ephemeral rootfs,
    not a PVC, this file does not survive pod teardown and cannot be
    read by anything outside the container after it exits.
```

## Skills

### What Gets Ported

| Existing | New location | Changes |
|---|---|---|
| `meta-skill/SKILL.md` | `skills/orchestrator/SKILL.md` | Update to read phases.json, call konveyor-push at phase boundaries |
| `skill-bundle/.../skills/migration-plan/SKILL.md` | `skills/plan/SKILL.md` | Reference paths change to skills dir |
| `recipes/execute.yaml` | `skills/execute/SKILL.md` | Convert from goose recipe YAML to SKILL.md format |
| `recipes/verify.yaml` + `recipes/fix.yaml` | `skills/verify/SKILL.md` | Merge into one skill: verify build, fix errors iteratively (max 3), re-verify |
| `skill-bundle/.../skills/javaee-quarkus/SKILL.md` | ImageVolume mount | No change — mounted as rule |
| `skill-bundle/.../skills/python2-to-python3/SKILL.md` | ImageVolume mount | No change — mounted as rule |
| `skill-bundle/.../references/*.md` | Bundled with rule skill ImageVolume | No change — reference material |

### Conversion Work

- **Recipes (YAML) to SKILL.md (markdown)**: The verify, execute, and fix
  recipes are goose recipe format. The instructions and prompts port
  directly into SKILL.md frontmatter + markdown body.
- **Path updates**: Skills reference `/workspace/repo/` for the workspace.
  Rule skills and references load from the configurable skills directory.
- **Push integration**: Each skill gets instructions to call `konveyor-push`
  at appropriate checkpoints.
- **Migration knowledge preserved**: How to plan, how to transform, what
  verify commands to run — all stays unchanged. Only packaging changes.

### Skills Path

Configurable via `KONVEYOR_SKILLS_DIR`:

```
Container:     KONVEYOR_SKILLS_DIR=/opt/skills/
Local dev:     KONVEYOR_SKILLS_DIR=./skills/
```

The meta-skill resolves phase skill paths relative to this directory.
phases.json uses relative paths (e.g., `plan/SKILL.md`).

## Harness Lifecycle

Full sequence from container start to exit:

```
Container starts → konveyor-harness run
│
├─ 1. READ CONFIG
│    Read KONVEYOR_PARAM_SOURCE_URL
│    Read KONVEYOR_PARAM_TARGET_BRANCH
│    Read KONVEYOR_INSTRUCTIONS (NOT a KONVEYOR_PARAM_* — see note below)
│    Read KONVEYOR_SKILLS_DIR (default: /opt/skills/)
│
├─ 2. CLONE
│    konveyor-clone $SOURCE_URL /workspace/repo
│    cd /workspace/repo
│    git checkout -B $TARGET_BRANCH
│
├─ 3. CONFIGURE
│    konveyor-configure
│    Writes goose config from LLM credential env vars
│
├─ 4. DETECT (Go, no LLM)
│    konveyor-detect /workspace/repo
│    → detect.json, graph.json written to /workspace/repo/
│    konveyor-push --message "konveyor: detect phase" detect.json graph.json
│
├─ 5. WRITE INSTRUCTIONS + GENERATE PHASES
│    Write /workspace/instructions.md from KONVEYOR_INSTRUCTIONS
│    (written OUTSIDE /workspace/repo/ — not git-tracked, never pushed)
│    Write /workspace/repo/phases.json:
│    [
│      {"name":"plan",    "skill":"plan/SKILL.md",    "expected_outputs":["PLAN.md"]},
│      {"name":"execute", "skill":"execute/SKILL.md", "expected_outputs":["execution-log.md"]},
│      {"name":"verify-fix", "skill":"verify/SKILL.md", "expected_outputs":["verify-report.md"],
│       "description": "Verify build, then fix errors iteratively (max 3 iterations, internal to this skill)."}
│    ]
│
├─ 6. LAUNCH GOOSE AND DRIVE THE SESSION
│    goose serve --port 4000 &
│    Poll /acp with a bare GET until it responds (process is listening)
│    Dial ws://localhost:4000/acp — capture Acp-Connection-Id from the
│      upgrade response header
│    JSON-RPC: initialize → session/new (cwd=/workspace/repo) → session_id
│    JSON-RPC: session/prompt (BLOCKS until the turn completes): load
│      orchestrator skill, pass instructions.md contents and phases.json path
│    Meta-skill reads phases.json, sequences LLM phases
│    Skills call konveyor-push at meaningful checkpoints
│    Before the session ends, the orchestration skill writes and pushes
│      .konveyor/handoff.md itself
│    session/prompt's response carries stopReason + usage directly —
│      no separate stream-consumption loop needed for completion or usage
│    Harness checks phases.json's expected_outputs on disk to determine
│      steps_completed / steps_failed
│    Harness sends SIGTERM to goose serve, waits for it to stop
│    (see "Launching and Driving Goose" for full detail)
│
├─ 7. FINAL PUSH
│    Write .konveyor/session.json (harness-authored — timing, tokens,
│      step completion; handoff.md was already written+pushed in step 6)
│    konveyor-push --message "konveyor: session metadata" \
│      .konveyor/session.json
│
└─ 8. WRITE RESULTS
     konveyor-results --exit-code $SESSION_EXIT_CODE
     → /.konveyor/results.json (pod-local fallback only — lives on the
       container's ephemeral rootfs, not a PVC, so it does not survive
       pod teardown; the controller's real status channel is ACP, not
       this file — see "Observability Ownership")
     Exit
```

### POC Simplifications

- No smart phase skipping — every run does all steps (detect through verify-fix).
- Smart detection of existing artifacts on the branch is a future enhancement.
- handoff.md and session.json are still written (cross-stage contract from
  PR #295).

### Why KONVEYOR_INSTRUCTIONS, not KONVEYOR_PARAM_INSTRUCTIONS

Per PR #295, the controller injects declared Agent `params` as
`KONVEYOR_PARAM_{NAME}` env vars. AgentRun's `instructions` field is
explicitly a separate, distinct field ("composed with prompt") — not
one of the declared params. The controller must inject it as its own
env var (`KONVEYOR_INSTRUCTIONS`), not under the `KONVEYOR_PARAM_*`
convention. This needs to be confirmed with the PR #295 controller
implementation once it exists.

## Session Handoff

Committed to `.konveyor/` in the repo. Two files, two authors, two
different points in the lifecycle:

### handoff.md

Human-readable summary for the next stage's LLM. Written AND pushed by
the **orchestration skill** (not the harness) as its last action before
the session ends — only the LLM knows what it did, what worked, and
what the next stage needs to know. The harness never touches this file.

```markdown
# Stage Handoff: {stage_name}

## Status
{complete | failed}

## What Was Done
- {summary of work completed}

## What Needs to Happen Next
- {remaining work}

## Key Findings
- {observations, warnings}
```

### session.json

Machine-readable metadata. Written by the **harness** (not the skill)
because it has timing, token usage, and phase completion data:

```json
{
  "session_id": "uuid",
  "status": "complete",
  "started_at": "2026-07-01T10:00:00Z",
  "completed_at": "2026-07-01T10:45:00Z",
  "duration_seconds": 2700,
  "runtime": "goose",
  "models": [
    {
      "role": "primary",
      "provider": "anthropic",
      "name": "claude-sonnet-4-20250514",
      "token_usage": {
        "input_tokens": 125000,
        "output_tokens": 45000
      }
    }
  ],
  "steps_completed": ["detect", "plan", "execute", "verify-fix"],
  "steps_failed": [],
  "git": {
    "target_branch": "konveyor/migrate-app-123",
    "commits": 12,
    "last_commit_sha": "abc1234"
  }
}
```

`steps_completed`/`steps_failed` has two sources, not one: `detect` is
tracked directly by the harness from `konveyor-detect`'s own exit code
(it's a Go call the harness makes itself, not a phases.json entry).
`plan`/`execute`/`verify-fix` come from checking phases.json's
`expected_outputs` on disk after `session/prompt`'s blocking response
returns, per "Launching and Driving Goose."

Matches PR #295's model-role convention (`primary`/`efficient`/`planner`,
`AgentRun.spec.models` as a list keyed by role) — `models` is a list so
runs using multiple models by role attribute token usage correctly.
The `stage` field was dropped: this document has no defined stage
vocabulary yet (that belongs to the AgentPlaybook design, out of POC
scope), and a stale example value here caused confusion in review.

### results.json (pod-local)

Written by `konveyor-results` to `/.konveyor/results.json`. NOT
committed to git. This is a pod-local fallback for tooling that
inspects the running or recently-exited container directly (e.g.
standalone CLI-style runs without a controller attached) — it is NOT a
reliable channel for the controller, which reads status via the ACP
connection instead (see "Observability Ownership" above). `/.konveyor/`
lives on the container's ephemeral rootfs, not a PVC, so this file does
not survive pod teardown and cannot be read by anything outside the
container after it exits.

```json
{
  "status": "succeeded",
  "exit_code": 0,
  "duration_seconds": 2700,
  "git": {
    "target_branch": "konveyor/migrate-app-123",
    "commits": 12,
    "last_commit_sha": "abc1234"
  }
}
```

## Push Model

Two callers, one utility:

### Harness-driven pushes (Go, deterministic)

| When | What | Message |
|---|---|---|
| After detect | detect.json, graph.json | `konveyor: detect phase` |
| On exit | .konveyor/session.json | `konveyor: session metadata` |

### Skill-driven pushes (goose calls konveyor-push)

| When | What | Message |
|---|---|---|
| After plan writes PLAN.md | PLAN.md | `konveyor: plan phase` |
| After each file migrated | changed files | `konveyor: migrate {filename}` |
| After verify-fix loop | verify-report.md, fixed files | `konveyor: verify-fix phase` |
| Before session ends | .konveyor/handoff.md | `konveyor: stage handoff` |

Both use the same `konveyor-push` binary. Credentials come from env
vars injected via envFrom/secretRef (see konveyor-clone/konveyor-push).
The LLM never sees credentials directly — it only invokes the
`konveyor-push` CLI tool, which reads them itself.

## Image Hierarchy

### POC: One image with everything

For the POC, build a single image with goose and a language toolchain:

```dockerfile
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

# System tools
RUN microdnf install -y git jq curl python3 python3-pip && microdnf clean all

# graphify
RUN pip3 install --no-cache-dir graphifyy==0.7.17

# goose
RUN curl -fsSL <goose-release-url> | tar -xj -C /usr/local/bin/

# skillctl (skill discovery, from skillimage project)
RUN curl -fsSL <skillctl-release-url> -o /usr/local/bin/skillctl \
    && chmod +x /usr/local/bin/skillctl

# Go binaries (built in CI)
COPY bin/konveyor-*  /usr/local/bin/

# GIT_ASKPASS helper for konveyor-push (echoes KONVEYOR_GIT_TOKEN to stdout)
COPY scripts/git-askpass.sh /usr/local/bin/git-askpass.sh
RUN chmod +x /usr/local/bin/git-askpass.sh

# Pipeline skills
COPY skills/ /opt/skills/

# Writable dirs for non-root
RUN mkdir -p /workspace /.konveyor \
    && chmod 775 /workspace /.konveyor \
    && chgrp -R 0 /workspace /.konveyor

WORKDIR /workspace
ENTRYPOINT ["konveyor-harness"]
CMD ["run"]
```

### Long-term: Layered hierarchy (from PR #296)

```
agent-base                    UBI 9 + Go binaries + skillctl + graphify + skills
  └── agent-base-goose        + goose, ENTRYPOINT konveyor-harness
        └── agent-java-goose  + JDK 21 + Maven
        └── agent-node-goose  + Node.js 20
        └── agent-full-goose  + all languages
  └── agent-base-opencode     + opencode, ENTRYPOINT konveyor-harness
```

agent-base has NO entrypoint — it's a building block. The entrypoint
lives on runtime images (agent-base-goose, agent-base-opencode) because
`konveyor-harness run` requires a runtime to launch.

## Repo Structure

```
migration-harness/
  cmd/
    harness/main.go           # konveyor-harness (entrypoint)
    clone/main.go             # konveyor-clone
    push/main.go              # konveyor-push
    configure/main.go         # konveyor-configure
    detect/main.go            # konveyor-detect
    results/main.go           # konveyor-results
  internal/
    git/                      # shared git operations, credential handling
    config/                   # env var parsing, runtime config generation
    phases/                   # phases.json generation
  skills/
    orchestrator/SKILL.md     # meta-skill — reads phases.json, sequences
    plan/SKILL.md             # ported from skill-bundle migration-plan
    execute/SKILL.md          # ported from recipes/execute.yaml
    verify/SKILL.md           # merged verify + fix: build, fix loop (max 3), re-verify
  dockerfiles/
    agent-base.Dockerfile
    agent-base-goose.Dockerfile
    agent-java-goose.Dockerfile
  scripts/
    git-askpass.sh            # GIT_ASKPASS helper, echoes KONVEYOR_GIT_TOKEN
  go.mod
  go.sum
```

Existing code moves to `_legacy/` before development starts to avoid
confusion:

```
_legacy/
  bin/migration-harness
  lib/step-detect.sh
  lib/step-plan.sh
  lib/step-execute.sh
  lib/step-verify.sh
  lib/step-fix-loop.sh
  lib/common.sh
  lib/metrics.sh
  lib/build-graph.py
  recipes/
  skill-bundle/
  Dockerfile
  docker-compose.yml
  install.sh
```

The migration knowledge in skills and recipes is ported into the new
`skills/` directory. `_legacy/` is removed once the new code is proven.

## CRD Mapping

How this harness maps to the CRDs from PR #295:

### Agent CR

```yaml
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata:
  name: java-migration-agent
spec:
  image: quay.io/konveyor/agent-java-goose:latest
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
    - name: target_branch
      type: string
```

Pipeline skills (orchestrator, plan, execute, verify — verify includes
the fix loop) are baked into the image. Only language-specific rule
skills (javaee-quarkus) appear as SkillCard refs. See the note on
this deviating from PR #296 in Key Decisions and Open Questions.

**No mount collision**: per PR #295, SkillCard ImageVolumes mount at
`/opt/skills/{name}/` — a named subdirectory, not the `/opt/skills/`
root. Baked-in pipeline skills already live in named subdirectories
(`orchestrator/`, `plan/`, `execute/`, `verify/`). A rule skill like
`javaee-quarkus` mounts at `/opt/skills/javaee-quarkus/`, alongside
the baked-in skills, not over them.

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

The `git-write-creds` Secret's keys must be named `KONVEYOR_GIT_USERNAME`
and `KONVEYOR_GIT_TOKEN` — `envFrom`/`secretRef` injects env vars using
the Secret's own key names verbatim, so `konveyor-clone`/`konveyor-push`
only find credentials if the Secret was created with those exact keys.

The controller creates a Sandbox, injects `params` as `KONVEYOR_PARAM_*`
env vars and `instructions` as `KONVEYOR_INSTRUCTIONS` (see "Why
KONVEYOR_INSTRUCTIONS" above), mounts SkillCard ImageVolumes at
`/opt/skills/{name}/` (a named subdirectory per skill, not the root —
see "No mount collision" above), and launches the container.
konveyor-harness takes over from there.

## Open Questions (Resolved)

| Question | Resolution |
|---|---|
| Orchestration model | Meta-skill reads phases.json, sequences phase skills (option B) |
| Entrypoint language | Go binary (konveyor-harness) |
| Utility language | All Go |
| Detect phase | Go utility (konveyor-detect), no LLM |
| What to port | Extract migration knowledge from skills/recipes, don't preserve bash |
| Workspace persistence | EmptyDir, git is persistence layer |
| Push model | Both harness-driven (phase boundaries) and skill-driven (mid-phase) |
| agent-base entrypoint | No entrypoint on agent-base; entrypoint on runtime images |
| POC scope | No smart phase skipping, full pipeline every run |
| goose invocation | **CONFIRMED**: `goose serve` + WebSocket + JSON-RPC 2.0. `session/prompt` blocks until the turn completes and its response carries `stopReason`+`usage` directly — no event-stream-consumption loop needed for completion detection. See "Launching and Driving Goose" |
| Session termination / goose serve lifecycle | Harness SIGTERMs goose serve after `session/prompt` returns, before exiting. Controller observability (SSE) only works while the harness is alive and the WebSocket connection is open, not after |
| Step-level completion tracking | Harness checks phases.json's `expected_outputs` on disk after `session/prompt` returns — its response only reports overall turn status (`stopReason`), not per-phase status |
| Token usage source | **CONFIRMED**: `session/prompt`'s blocking JSON-RPC response carries `usage: {totalTokens, inputTokens, outputTokens}` directly — no stream accumulation needed |
| Skill baking vs PR #296 | Pipeline skills baked into image for POC (deviates from PR #296's "never baked in" model). Intentional, to revisit with PR #296 authors. No mount collision — SkillCards mount at `/opt/skills/{name}/`, a subdirectory |
| instructions.md location | Written to `/workspace/` (not `/workspace/repo/`) so it's never git-tracked or accidentally pushed |
| KONVEYOR_INSTRUCTIONS | Separate env var from `KONVEYOR_PARAM_*`, matching PR #295's distinction between params and instructions — needs confirmation once the controller is implemented |
| Git credential delivery | env vars via envFrom/secretRef (matches PR #295's AgentRun examples), not a mounted Secret file path. Push-time re-auth via GIT_ASKPASS helper script |
| session.json model schema | `models` is a list keyed by `role` (matches PR #295's primary/efficient/planner convention), each with its own token_usage — not a single flat model+usage pair |
| Controller/UI observability via `/acp` | **CONFIRMED, narrower than assumed**: a controller can attach a read-only SSE observer using the harness's `Acp-Connection-Id`, but it only relays completed request/response pairs, not live `session/update` notifications. How the controller obtains the connection ID is still unresolved — not yet exposed anywhere it can read it |
| results.json fate | Kept as a pod-local **fallback** (not the controller's primary status source, which is now ACP) — for standalone runs without a controller attached |
| WebSocket client dependency | `github.com/coder/websocket` (ISC/OSS) — the one exception to the stdlib-only rule, since Go's standard library has no WebSocket client and ACP over `goose serve` requires one |
| Context budget enforcement | Explicitly out of scope for the POC per the controller enhancement — the harness has no logic for this and should not gain any |
