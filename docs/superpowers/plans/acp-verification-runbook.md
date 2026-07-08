# ACP Mechanism Verification Runbook — COMPLETED

**Status: DONE.** This runbook was originally written as a pre-flight
checklist to run manually against a real `goose serve` instance before
relying on `internal/acp` for a production migration. That verification
has since been performed. The real protocol turned out to be completely
different from the curl-based HTTP POST/GET hypothesis this runbook
started with — see "What Was Actually Verified" below for the confirmed
findings, and the "Original (superseded) hypothesis" section at the end
for the discarded starting point, kept only for historical context.

The confirmed findings are also documented in
`docs/superpowers/specs/2026-07-02-harness-restructure-design.md`'s
"Launching and Driving Goose" and "Controller Observability (SSE)"
sections, and are implemented in `internal/acp/client.go`. This file is
no longer an open TODO — it's a record of what was checked and what was
found.

## What Was Actually Verified

Against a real `goose serve` instance:

1. **Transport is WebSocket, not plain HTTP POST/GET.** `goose serve`'s
   `/acp` endpoint requires a WebSocket upgrade handshake. A bare HTTP
   GET or POST to `/acp` (no WebSocket upgrade) returns 4xx errors
   demanding either an `Accept: text/event-stream` header or an
   already-established `Acp-Connection-Id` — that's the read-only
   *observer* path (see point 5), not how a client drives the session.
   A bare GET is still useful as a readiness probe: any response at all
   (even a 4xx) confirms the process is listening; connection-refused
   means it isn't up yet. This is what `internal/acp.WaitReady` does.

2. **The server returns `Acp-Connection-Id` as a response header on the
   WebSocket upgrade (`101 Switching Protocols`) response.** This ID is
   what a controller needs to attach a read-only SSE observer to the
   same session later (see point 5). `internal/acp.Client.Connect`
   captures it from `resp.Header.Get("Acp-Connection-Id")`.

3. **All driving happens as JSON-RPC 2.0 messages over the single
   WebSocket connection.** Requests/responses and server-initiated
   notifications are multiplexed on the same socket. Confirmed sequence:

   ```
   → {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1.0","clientInfo":{...}}}
   ← {"jsonrpc":"2.0","result":{"agentCapabilities":{...},"authMethods":[...]},"id":1}

   → {"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/workspace/repo","mcpServers":[]}}
   ← {"jsonrpc":"2.0","result":{"sessionId":"20260708_2","modes":{...},"models":{...}},"id":2}
   (sessionId is a date-based string, e.g. "20260708_2" — not a UUID)

   → {"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"20260708_2","prompt":[{"type":"text","text":"..."}]}}
   (BLOCKS — does not return until the full turn completes)
   ... session/update notifications may arrive on the same socket while waiting, optional to consume ...
   ← {"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"totalTokens":11286,"inputTokens":11238,"outputTokens":48}},"id":3}
   ```

   This matches `internal/acp/client.go`'s `Initialize`, `NewSession`,
   and `Prompt` methods exactly (method names, param shapes, and result
   field names).

4. **`session/prompt` blocks until the turn completes — it is not
   fire-and-forget.** This was the single biggest wrong assumption in
   the original hypothesis below. The JSON-RPC response only arrives
   once the agent's full turn finishes, and it carries `stopReason` and
   `usage` (`totalTokens`/`inputTokens`/`outputTokens`) directly. No
   separate event-stream-consumption loop is needed to detect
   completion or collect token usage — awaiting the blocking RPC call
   is sufficient. `session/update` notifications (message chunks,
   thought chunks, live usage snapshots) still arrive on the same
   WebSocket during the turn; consuming them (via
   `internal/acp.Client.Notifications`) is optional — useful only for
   live progress logging, not required for correctness. Only
   `"end_turn"` was observed as a success `stopReason`; other values
   (turn limits, errors, refusals) were not observed during
   verification and are treated as failures (fail-closed) until real
   failure modes are seen in practice.

5. **The SSE observer path exists but only relays completed
   request/response pairs, not live notifications.** `GET /acp` with
   `Accept: text/event-stream` and the `Acp-Connection-Id` header (set
   to the value from the driving WebSocket's upgrade response, point 2)
   opens a real SSE stream (confirmed: status 200,
   `content-type: text/event-stream`). Verified twice, including with
   raw unbuffered reads and explicit timestamps to rule out a buffering
   artifact: the SSE observer saw the `initialize` result, the
   `session/new` result, and the final `session/prompt` result (each
   exactly matching what arrived on the driving WebSocket), but received
   **zero** of the interleaved `session/update` notifications that
   arrived on the WebSocket during the same window. A controller
   watching via SSE sees "session created" and "turn completed, here's
   the token count," not live token-by-token progress. This is
   sufficient for the controller's actual need (lifecycle-transition CR
   updates, not live streaming — see the design spec's "Observability
   Ownership" section), but is a real, confirmed limitation if fuller
   live observability is ever wanted.

## Re-verification

This is not an open TODO — treat it as done unless you have a specific
reason to suspect regression (e.g. goose ships a new ACP implementation
version, or `internal/acp/client.go`'s behavior stops matching
production). If you do suspect a regression, re-run against a live
`goose serve` instance and check whether these assumptions still hold:

- Is the wire format still JSON-RPC 2.0 over a single WebSocket
  connection (not HTTP POST/GET, not plain SSE for driving)?
- Does `session/prompt` still block until the turn completes, or has it
  become fire-and-forget (which would require re-introducing an
  event-stream-consumption loop for completion detection)?
- Does the `session/prompt` response still carry `stopReason` and
  `usage` (`totalTokens`/`inputTokens`/`outputTokens`) directly?
- Does the SSE observer path still relay only completed request/response
  pairs, or does it now also relay live `session/update` notifications?

If any of these have changed, update `internal/acp/client.go` (and its
tests) to match, and update the design spec's "Launching and Driving
Goose" / "Controller Observability (SSE)" sections accordingly.

## Original (superseded) hypothesis

This is the curl-based, HTTP POST/GET procedure the investigation
started from, before a real `goose serve` instance was actually driven.
It is **not** what was found — kept only for historical context on how
the investigation began. Do not follow these steps as instructions;
see "What Was Actually Verified" above for the real procedure and
results.

<details>
<summary>Original hypothesis (click to expand) — HTTP POST/GET, proven wrong</summary>

1. Start goose serve and inspect its actual endpoints:

   ```bash
   GOOSE_SERVER__SECRET_KEY=test-key goose serve --port 4000 &
   curl -v http://localhost:4000/acp
   ```

   Hypothesis at the time: `/acp` might respond directly over plain
   HTTP. Actual finding: it requires a WebSocket upgrade; a bare GET
   without upgrade returns a 4xx demanding `Accept: text/event-stream`
   or an `Acp-Connection-Id` (that's the SSE observer path, not the
   driving path).

2. Attempt to create a session assuming a plain HTTP POST:

   ```bash
   curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/new"}'
   ```

   Hypothesis at the time: a bare `{"method":"session/new"}` JSON body
   over POST. Actual finding: `session/new` is a JSON-RPC 2.0 request
   sent over the WebSocket connection, with `params: {cwd, mcpServers}`,
   not a bare HTTP POST body.

3. Attempt to prompt the session assuming fire-and-forget POST:

   ```bash
   time curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/prompt","params":{"session_id":"<id-from-step-2>","message":"say hello"}}'
   ```

   Hypothesis at the time: this might return immediately
   (fire-and-forget). Actual finding: `session/prompt` is a JSON-RPC
   call over the WebSocket that BLOCKS until the agent's turn
   completes, and its params are `{sessionId, prompt: [{type, text}]}`,
   not `{session_id, message}`.

4. Attempt to open a separate polling event stream:

   ```bash
   curl -N http://localhost:4000/acp/sessions/<id>/events
   ```

   Hypothesis at the time: a per-session REST-style events endpoint.
   Actual finding: no such endpoint exists. The real mechanism is
   `session/update` JSON-RPC notifications multiplexed on the same
   driving WebSocket (optional to consume), plus a separate read-only
   SSE observer at `GET /acp` (with `Accept: text/event-stream` and
   `Acp-Connection-Id`) that only relays completed request/response
   pairs, not live notifications.

5. Token usage: hypothesized it might appear in a stream event or
   nowhere. Actual finding: it appears directly in `session/prompt`'s
   blocking JSON-RPC response, under `usage: {totalTokens, inputTokens,
   outputTokens}`.

</details>
