// Package acp implements a client for goose's ACP (Agent Client Protocol)
// endpoint. Confirmed against a real goose serve instance -- see
// docs/superpowers/plans/acp-verification-runbook.md and the design
// spec's "Launching and Driving Goose" section. The protocol is JSON-RPC
// 2.0 over a single WebSocket connection: requests/responses and
// server-initiated notifications are multiplexed on the same socket.
// session/prompt blocks until the agent's turn completes and its
// response carries stopReason and usage directly -- no separate
// stream-consumption loop is needed to detect completion or collect
// token usage.
package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// rpcEnvelope is any JSON-RPC 2.0 message as received -- a response (has
// ID, and Result or Error) or a notification (has Method, no ID).
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

// Notification is a server-initiated session/update message -- live
// progress (message chunks, thought chunks, usage snapshots). Consuming
// these via Client.Notifications is optional; Prompt's return value is
// sufficient to detect completion and collect final usage.
type Notification struct {
	Method string
	Params json.RawMessage
}

// Client drives a goose serve instance's ACP endpoint over a single
// WebSocket connection using JSON-RPC 2.0.
type Client struct {
	conn         *websocket.Conn
	connectionID string

	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan rpcEnvelope
	closed  bool

	notifications chan Notification
}

// WaitReady polls baseURL's /acp path with a bare HTTP GET until it gets
// any response (even a 4xx -- that still means the process is listening)
// or timeout elapses. Call this before Connect, since Connect requires
// goose serve to already be accepting connections. Each individual
// attempt is bound to the overall deadline via context, so one hung
// request can't push total elapsed time past timeout.
func WaitReady(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	client := &http.Client{}
	for time.Now().Before(deadline) {
		reqCtx, cancel := context.WithDeadline(ctx, deadline)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/acp", nil)
		if err != nil {
			cancel()
			return fmt.Errorf("build readiness request: %w", err)
		}
		resp, err := client.Do(req)
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

// Connect dials baseURL's /acp endpoint over WebSocket (rewriting
// http(s):// to ws(s):// itself) and starts the background read loop
// that demultiplexes responses (matched by id) from notifications.
func Connect(ctx context.Context, baseURL string) (*Client, error) {
	wsURL, err := toWebSocketURL(baseURL)
	if err != nil {
		return nil, err
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial acp websocket: %w", err)
	}

	c := &Client{
		conn:          conn,
		connectionID:  resp.Header.Get("Acp-Connection-Id"),
		pending:       make(map[int64]chan rpcEnvelope),
		notifications: make(chan Notification, 64),
	}
	go c.readLoop()
	return c, nil
}

func toWebSocketURL(baseURL string) (string, error) {
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		return "wss://" + strings.TrimPrefix(baseURL, "https://") + "/acp", nil
	case strings.HasPrefix(baseURL, "http://"):
		return "ws://" + strings.TrimPrefix(baseURL, "http://") + "/acp", nil
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", baseURL)
	}
}

// ConnectionID returns the Acp-Connection-Id assigned by the server
// during the WebSocket handshake.
func (c *Client) ConnectionID() string {
	return c.connectionID
}

// Notifications returns the channel of server-initiated session/update
// messages. Consuming this is optional. The channel closes when the
// connection's read loop exits (on any read error, including a clean
// close). The channel is buffered; if the consumer falls behind,
// excess notifications are dropped rather than blocking the read loop,
// since Prompt's return value -- not notifications -- is what determines
// correctness.
func (c *Client) Notifications() <-chan Notification {
	return c.notifications
}

func (c *Client) readLoop() {
	defer close(c.notifications)
	ctx := context.Background()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.failAllPending(err)
			return
		}
		var env rpcEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue // skip malformed frames rather than crash the reader
		}
		if env.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*env.ID]
			if ok {
				delete(c.pending, *env.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- env
			}
			continue
		}
		if env.Method != "" {
			select {
			case c.notifications <- Notification{Method: env.Method, Params: env.Params}:
			default:
			}
		}
	}
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	failure := rpcEnvelope{Error: &rpcError{Code: -1, Message: fmt.Sprintf("connection closed: %v", err)}}
	for id, ch := range c.pending {
		ch <- failure
		delete(c.pending, id)
	}
}

// call sends a JSON-RPC request and blocks until its matching response
// arrives, ctx is done, or the connection fails.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	respCh := make(chan rpcEnvelope, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("acp client is closed")
	}
	c.pending[id] = respCh
	c.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("marshal %s request: %w", method, err)
	}

	if err := c.conn.Write(ctx, websocket.MessageText, body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("send %s request: %w", method, err)
	}

	select {
	case env := <-respCh:
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, env.Error)
		}
		return env.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Initialize performs the ACP handshake. Must be called once, before
// NewSession or Prompt.
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "0.1.0",
		"clientInfo":      map[string]string{"name": "konveyor-harness", "version": "1"},
	})
	return err
}

// NewSession creates a session rooted at cwd and returns its ID.
func (c *Client) NewSession(ctx context.Context, cwd string) (string, error) {
	result, err := c.call(ctx, "session/new", map[string]interface{}{
		"cwd":        cwd,
		"mcpServers": []interface{}{},
	})
	if err != nil {
		return "", err
	}
	var parsed struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("parse session/new result: %w", err)
	}
	return parsed.SessionID, nil
}

// TokenUsage is session/prompt's final token accounting for one turn.
type TokenUsage struct {
	TotalTokens  int `json:"totalTokens"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// PromptResult is session/prompt's response -- confirmed to arrive only
// once the agent's turn fully completes.
type PromptResult struct {
	StopReason string     `json:"stopReason"`
	Usage      TokenUsage `json:"usage"`
}

// Prompt sends a text prompt to sessionID and BLOCKS until the agent's
// turn completes (confirmed behavior -- not fire-and-forget). The
// returned PromptResult carries the stop reason and token usage
// directly.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (PromptResult, error) {
	result, err := c.call(ctx, "session/prompt", map[string]interface{}{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		return PromptResult{}, err
	}
	var pr PromptResult
	if err := json.Unmarshal(result, &pr); err != nil {
		return PromptResult{}, fmt.Errorf("parse session/prompt result: %w", err)
	}
	return pr, nil
}

// Close closes the underlying WebSocket connection.
func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.conn.Close(websocket.StatusNormalClosure, "")
}
