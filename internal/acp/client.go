// Package acp implements a client for goose's ACP (Agent Client Protocol)
// endpoint. The exact wire protocol (method names, event shapes, terminal
// semantics) is UNVERIFIED against goose's real implementation — see the
// design spec's "Known Unknowns — Requires Prototyping" section. This
// client is built against the best-documented assumption (JSON-RPC-style
// POST calls, newline-delimited JSON events over a streamed HTTP GET) and
// is deliberately isolated behind this package so the assumption can be
// swapped out without touching cmd/harness.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event is one message from a session's event stream.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// IsTerminal reports whether e signals the end of a session.
func IsTerminal(e Event) bool {
	return e.Type == "complete" || e.Type == "failed"
}

type usageData struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// UsageFromEvent extracts token counts from a "usage" event. Returns
// (0, 0) for any other event type or malformed data.
func UsageFromEvent(e Event) (input, output int) {
	if e.Type != "usage" {
		return 0, 0
	}
	var u usageData
	if err := json.Unmarshal(e.Data, &u); err != nil {
		return 0, 0
	}
	return u.InputTokens, u.OutputTokens
}

// Client drives a goose serve instance's ACP endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// WaitReady polls the base ACP endpoint until it responds or timeout elapses.
func (c *Client) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/acp", nil)
		if err != nil {
			cancel()
			return fmt.Errorf("build readiness request: %w", err)
		}
		resp, err := c.http.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("acp endpoint not ready after %s: %w", timeout, lastErr)
}

type rpcRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type newSessionResult struct {
	SessionID string `json:"session_id"`
}

// NewSession creates a new goose session and returns its ID.
func (c *Client) NewSession() (string, error) {
	body, err := json.Marshal(rpcRequest{Method: "session/new"})
	if err != nil {
		return "", fmt.Errorf("marshal session/new request: %w", err)
	}
	resp, err := c.http.Post(c.baseURL+"/acp", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("session/new request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session/new returned status %d", resp.StatusCode)
	}
	var result newSessionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session/new response: %w", err)
	}
	return result.SessionID, nil
}

type promptParams struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// Prompt sends the initial message to a session. This is fire-and-forget —
// it acks receipt and does not block for the full pipeline duration.
// Callers must consume Stream to know when the session actually finishes.
func (c *Client) Prompt(sessionID, message string) error {
	body, err := json.Marshal(rpcRequest{
		Method: "session/prompt",
		Params: promptParams{SessionID: sessionID, Message: message},
	})
	if err != nil {
		return fmt.Errorf("marshal session/prompt request: %w", err)
	}
	resp, err := c.http.Post(c.baseURL+"/acp", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("session/prompt request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("session/prompt returned status %d", resp.StatusCode)
	}
	return nil
}

// Stream opens the session's event stream and returns a channel of events.
// The channel closes when a terminal event arrives, the stream ends, or
// ctx is cancelled — whichever happens first. Malformed lines are skipped
// rather than crashing the reader.
func (c *Client) Stream(ctx context.Context, sessionID string) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/acp/sessions/"+sessionID+"/events", nil)
	if err != nil {
		return nil, fmt.Errorf("build stream request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("event stream returned status %d", resp.StatusCode)
	}

	events := make(chan Event)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var e Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}
			select {
			case events <- e:
			case <-ctx.Done():
				return
			}
			if IsTerminal(e) {
				return
			}
		}
		// Only surface a scanner error as a stream_error event if it wasn't
		// caused by our own context being cancelled — cancellation aborts
		// the underlying read and shows up as a scanner error too, but
		// that's an intentional, clean stop, not a genuine transport
		// failure worth reporting.
		if scanErr := scanner.Err(); scanErr != nil && ctx.Err() == nil {
			errEvent := Event{
				Type: "stream_error",
				Data: json.RawMessage(fmt.Sprintf(`{"error":%q}`, scanErr.Error())),
			}
			select {
			case events <- errEvent:
			case <-ctx.Done():
			}
		}
	}()
	return events, nil
}
