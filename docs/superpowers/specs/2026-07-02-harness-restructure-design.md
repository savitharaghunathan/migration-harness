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
│  │  7. Launch goose serve, open session, subscribe to │  │
│  │     its event stream, send session/prompt (ack)    │  │
│  │  8. Consume stream until terminal event; check     │  │
│  │     phases.json outputs on disk; SIGTERM goose      │  │
│  │     serve and wait for it to stop                  │  │
│  │  9. konveyor-push (session.json; skill already     │  │
│  │     pushed handoff.md before session ended)        │  │
│  │ 10. konveyor-results (pod-local results)           │  │
│  └──────────────┬─────────────────────────────────────┘  │
│                 │ launches                                │
│                 ▼                                         │
│  ┌────────────────────────────────────────────────────┐  │
│  │  goose serve (agent runtime, ACP over HTTP)        │  │
│  │  Harness drives the session over ACP (session/new, │  │
│  │  session/prompt) — same endpoint the controller/UI  │  │
│  │  use for observability                              │  │
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

`goose run` and `goose serve` start independent sessions — a `goose run`
process would not share state with a `goose serve` instance already
listening on port 4000. Since PR #295 requires `goose serve` for ACP
observability (controller and UI connect to `/acp`), the harness also
uses `goose serve` to drive the actual work, rather than shelling out to
a separate `goose run` invocation.

**The harness drives the session by consuming its event stream, not by
polling a status endpoint.** ACP exposes this over Streamable HTTP
and/or WebSocket — goose does not support the older SSE transport, per
goose's own docs — so `session/prompt` is treated as fire-and-forget
(acks receipt, does not block for the full pipeline duration), and the
harness's `internal/acp` client stays subscribed to the session's event
stream until it sees a terminal event. This resolves the
poll-vs-blocking-call ambiguity: there is no separate poll loop, there
is one continuously-consumed stream.

Also confirmed: `goose serve` requires an auth secret
(`GOOSE_SERVER__SECRET_KEY`, or `--dangerously-unauthenticated` for
local dev only) — `konveyor-configure` must set this before launch, see
konveyor-configure below.

```
1. Launch: GOOSE_SERVER__SECRET_KEY=<generated> goose serve --port 4000 &
2. Poll http://localhost:4000/acp until it responds (session endpoint ready)
3. ACP call: POST /acp  { method: "session/new" }  → session_id
4. Open the session's event stream (Streamable HTTP or WebSocket) and subscribe
5. ACP call: POST /acp  { method: "session/prompt", session_id,
     message: "Load skill $KONVEYOR_SKILLS_DIR/orchestrator/SKILL.md.
                Instructions: <contents of instructions.md>.
                phases.json is at /workspace/repo/phases.json." }
   (this call acks receipt — it does not block until the pipeline finishes)
6. Consume the event stream:
   - Accumulate token usage from usage-bearing events as they arrive
     (session.json's token_usage is a running total from the stream,
     not a single end-of-session value)
   - Watch for a terminal event (session complete or failed)
7. On terminal event: stop consuming, read /workspace/repo/ on disk to
   determine which of phases.json's expected_outputs actually exist —
   this is how steps_completed / steps_failed in session.json is built,
   since the stream's terminal event only reports overall session
   status, not per-phase status
8. Controller and UI may connect to the same /acp endpoint concurrently
   during this window (while the harness is subscribed) for
   observability — ACP's session/load replays history on connect
9. After the terminal event, before the harness exits: send SIGTERM to
   the backgrounded goose serve process and wait for it to stop (bounded
   timeout) — the container's lifetime is the harness's lifetime, so
   there is no window for observability after the harness decides to
   exit; goose serve must be stopped explicitly, not left to be killed
   by the container runtime tearing down the pod
```

**Needs verification during implementation**: the exact ACP method and
event names (`session/new`, `session/prompt`, the terminal event type,
whether usage data is actually emitted on the stream, `session/load`'s
replay behavior) are inferred from PR #295's description of the ACP
protocol and the high-level ACP/goose docs, not confirmed against
goose's actual wire protocol — neither goose's ACP client docs
(goose-docs.ai) nor the ACP spec's overview page (agentclientprotocol.com)
publish method names, payload shapes, or completion signaling at the
level of detail needed to implement `internal/acp`; that requires
reading goose's source or the ACP JSON schema directly. This whole
mechanism — event-stream-driven session control — is the biggest
unverified assumption in this spec and should be prototyped against a
real `goose serve` instance before committing to `internal/acp`'s
design. If goose's ACP implementation doesn't support streaming usage
or per-event granularity, the phases.json-expected-outputs fallback in
step 7 still works for step completion tracking, but token_usage would
need a different source (e.g., goose's own logs, or a metrics event we
haven't confirmed exists).

Also worth noting: the Agent Client Protocol's own overview
distinguishes local agents (stdio JSON-RPC — the mature, fully-supported
path) from remote agents (HTTP/WebSocket — explicitly described as
"a work in progress"). `goose serve`'s HTTP/WebSocket ACP endpoint,
which this design builds on for both driving and observability, falls
into the less-mature remote-agent category. This was a deliberate
choice (see Open Questions) to keep driving and observability unified
on one endpoint rather than splitting to a stdio-based `goose acp` for
driving — accepted as a real risk given the protocol's own
work-in-progress label on this transport.

### Known Unknowns — Requires Prototyping

The event-stream mechanism above is a design hypothesis, not a verified
design. The following are genuine open engineering questions that
prototyping against a real `goose serve` instance should answer —
deliberately not resolved further here, since guessing at mechanism
details without being able to test them just produces more prose to
contradict later:

- **Subscription race**: does `session/new` start agent work
  immediately, or only once `session/prompt` is sent? If the former,
  the harness must confirm its stream subscription is fully
  established before treating the session as ready, or early events
  (including early usage/failure signals) could be silently missed.
- **Transport**: confirmed goose does not support SSE — it's Streamable
  HTTP and/or WebSocket. These are still materially different to
  implement (chunked HTTP response parsing vs framed WebSocket
  messages, different reconnect semantics). Which one goose's ACP
  endpoint actually uses (or whether it's client-negotiable) needs to
  be confirmed before writing `internal/acp`'s stream client.
- **Error and reconnect handling**: if the stream connection drops
  before a terminal event (network blip, goose crash), does the
  harness reconnect and replay via `session/load`, treat it as a
  failure, or hang? A bounded timeout on stream consumption is a
  minimum requirement regardless of the answer, so the harness can
  always reach its own SIGTERM-and-exit path rather than depending on
  an external Kubernetes timeout to kill the pod.
- **Conflicting completion signals**: if the terminal event reports
  `failed` but expected_outputs exist on disk (or the reverse), which
  signal wins for session.json's top-level `status`? Needs a rule once
  real behavior is observed, not a guess now.
- **Per-role usage attribution**: if a run uses multiple models by
  role, can a usage-bearing stream event be attributed to a specific
  role, or does goose only report aggregate usage? This determines
  whether session.json's per-role `token_usage` (in the `models` list)
  is achievable as designed or needs to fall back to a single
  aggregate figure.
- **Controller/UI auth to `/acp`**: `goose serve` requires
  `GOOSE_SERVER__SECRET_KEY`, and konveyor-configure generates this
  randomly per-run, known only to the harness. As designed, there is
  no mechanism for the controller or UI to obtain this secret, so the
  "controller and UI may connect concurrently for observability" claim
  made earlier in this section does not actually work yet — either the
  secret needs to be surfaced somewhere the controller can read it
  (e.g. a well-known file path, or a value the controller itself
  generates and passes in via env rather than the harness generating
  it), or observability access requires a different mechanism entirely.
  This needs to be resolved before the observability half of this
  design can be considered real.

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
  - Generate a random GOOSE_SERVER__SECRET_KEY for this run and export it
    (goose serve requires this for auth; it's local to this pod/process —
    the harness generates and consumes it itself, controller/UI never see it
    unless a future design adds authenticated observability access)
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
konveyor-results --exit-code <N>
  - Write /.konveyor/results.json
  - Contains: status, exit_code, duration, git branch, last commit SHA
  - Pod-local only — NOT committed to git
  - Controller reads this after pod completion
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
│    Poll /acp until ready
│    session/new → session_id, open event stream, subscribe
│    session/prompt (fire-and-forget ack): load orchestrator skill,
│      pass instructions.md contents and phases.json path
│    Meta-skill reads phases.json, sequences LLM phases
│    Skills call konveyor-push at meaningful checkpoints
│    Before the session ends, the orchestration skill writes and pushes
│      .konveyor/handoff.md itself
│    Harness accumulates token usage from the event stream as it consumes it
│    Event stream emits terminal event (complete or failed)
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
     → /.konveyor/results.json (pod-local, controller reads it)
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
`expected_outputs` on disk after the session's terminal event, per
"Launching and Driving Goose."

Matches PR #295's model-role convention (`primary`/`efficient`/`planner`,
`AgentRun.spec.models` as a list keyed by role) — `models` is a list so
runs using multiple models by role attribute token usage correctly.
The `stage` field was dropped: this document has no defined stage
vocabulary yet (that belongs to the AgentPlaybook design, out of POC
scope), and a stale example value here caused confusion in review.

### results.json (pod-local)

Written by `konveyor-results` to `/.konveyor/results.json`. Read by the
controller after pod completion. NOT committed to git.

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
| goose invocation | `goose serve` + event-stream-driven ACP session (session/new, subscribe, fire-and-forget session/prompt, consume stream to terminal event) — not `goose run`, not polling. **Prototype before committing** — biggest unverified assumption in this spec, see "Launching and Driving Goose" and "Known Unknowns — Requires Prototyping" |
| Session termination / goose serve lifecycle | Harness SIGTERMs goose serve after the terminal event, before exiting. Observability window is only while the harness is alive and subscribed, not after |
| Step-level completion tracking | Harness checks phases.json's `expected_outputs` on disk after the terminal event — the ACP terminal event only reports overall session status, not per-phase |
| Token usage source | Accumulated from the event stream as consumed. **Unverified** whether goose's ACP stream actually emits usage events — fallback (e.g. goose logs) not yet designed |
| Skill baking vs PR #296 | Pipeline skills baked into image for POC (deviates from PR #296's "never baked in" model). Intentional, to revisit with PR #296 authors. No mount collision — SkillCards mount at `/opt/skills/{name}/`, a subdirectory |
| instructions.md location | Written to `/workspace/` (not `/workspace/repo/`) so it's never git-tracked or accidentally pushed |
| KONVEYOR_INSTRUCTIONS | Separate env var from `KONVEYOR_PARAM_*`, matching PR #295's distinction between params and instructions — needs confirmation once the controller is implemented |
| Git credential delivery | env vars via envFrom/secretRef (matches PR #295's AgentRun examples), not a mounted Secret file path. Push-time re-auth via GIT_ASKPASS helper script |
| session.json model schema | `models` is a list keyed by `role` (matches PR #295's primary/efficient/planner convention), each with its own token_usage — not a single flat model+usage pair |
