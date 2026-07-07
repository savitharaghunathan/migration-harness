# ACP Mechanism Verification Runbook

Run this manually against a real `goose serve` instance before relying on
`internal/acp` for a production migration. This confirms or corrects the
assumptions documented in the design spec's "Known Unknowns" section.

## Prerequisites

- `goose` installed and configured with a working LLM provider.
- `GOOSE_SERVER__SECRET_KEY` set to any value.

## Procedure

1. Start goose serve and inspect its actual endpoints:

   ```bash
   GOOSE_SERVER__SECRET_KEY=test-key goose serve --port 4000 &
   curl -v http://localhost:4000/acp
   ```

   Record: does `/acp` respond at all without auth, or does it require
   the secret key in a header/query param immediately? Compare against
   `internal/acp.Client.WaitReady`'s bare GET request.

2. Attempt to create a session. Try the assumed shape first:

   ```bash
   curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/new"}'
   ```

   Record the actual response shape. Compare against
   `internal/acp.newSessionResult{SessionID string}`. If the real method
   name or response field differs, update `internal/acp/client.go`'s
   `NewSession` accordingly and add a regression test to
   `internal/acp/client_test.go` documenting the real shape.

3. Attempt to prompt the session and observe whether it blocks:

   ```bash
   time curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/prompt","params":{"session_id":"<id-from-step-2>","message":"say hello"}}'
   ```

   Record: does this return immediately (fire-and-forget, as assumed) or
   does it block until the agent's turn completes? This determines
   whether `internal/acp.Client.Prompt`'s current fire-and-forget
   assumption is correct or needs to change to a blocking call (which
   would also mean `Stream` becomes unnecessary for detecting
   completion).

4. Attempt to open the event stream:

   ```bash
   curl -N http://localhost:4000/acp/sessions/<id>/events
   ```

   Record: is this even a real endpoint? If not, find the real mechanism
   for observing session progress (check goose's `--help`, its source, or
   the ACP spec's deeper pages beyond the overview at
   agentclientprotocol.com). Record the actual event format (newline-JSON,
   SSE `data:` lines, WebSocket frames) and whether a terminal event
   exists and what it's called.

5. Check whether token usage appears anywhere in the above — in a stream
   event, in the session/prompt response, or nowhere (requiring a
   different source like goose's own logs).

6. Update `internal/acp/client.go` and its tests to match what was
   actually observed. Update the design spec's "Known Unknowns" section
   to mark resolved items and remove the "unverified" caveats language
   for anything confirmed here.

## Outcome

This task is complete when `internal/acp`'s tests reflect real, observed
goose behavior rather than the spec's inferred assumption, OR when a
decision is made (and documented) to switch to `goose acp` (stdio) for
driving instead — see the design spec's discussion of that alternative.
