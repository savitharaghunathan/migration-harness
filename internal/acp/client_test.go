package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// --- Test 1: WaitReady succeeds once a server responds ---

func TestWaitReady_SucceedsOnceServerResponds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	if err := WaitReady(ctx, server.URL, 2*time.Second); err != nil {
		t.Fatalf("WaitReady failed: %v", err)
	}
}

// --- Test 2: WaitReady times out when nothing is listening ---

func TestWaitReady_TimesOutIfServerNeverResponds(t *testing.T) {
	ctx := context.Background()
	err := WaitReady(ctx, "http://127.0.0.1:1", 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected WaitReady to time out and return an error")
	}
}

// --- Test 3: Connect captures the Acp-Connection-Id header ---

func TestConnect_CapturesConnectionID(t *testing.T) {
	const wantID = "conn-abc-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set the header BEFORE websocket.Accept writes the upgrade response.
		w.Header().Set("Acp-Connection-Id", wantID)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()
		// Read until the client closes -- this lets the close handshake
		// complete promptly instead of timing out.
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	got := client.ConnectionID()
	// Close via CloseNow to avoid the close handshake delay -- we
	// already have the value we need.
	client.conn.CloseNow()

	if got != wantID {
		t.Fatalf("ConnectionID() = %q, want %q", got, wantID)
	}
}

// --- Test 4: Initialize sends a correct request and parses the response ---

func TestInitialize_SendsCorrectRequestAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}

		var req rpcEnvelope
		if err := json.Unmarshal(data, &req); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}
		if req.Method != "initialize" {
			t.Errorf("expected method 'initialize', got %q", req.Method)
		}

		resp := fmt.Sprintf(`{"jsonrpc":"2.0","result":{},"id":%d}`, *req.ID)
		if err := conn.Write(ctx, websocket.MessageText, []byte(resp)); err != nil {
			t.Errorf("server write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

// --- Test 5: NewSession sends cwd and mcpServers, returns sessionId ---

func TestNewSession_SendsCwdAndMcpServersReturnsSessionID(t *testing.T) {
	errCh := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			errCh <- fmt.Sprintf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		_, data, err := conn.Read(ctx)
		if err != nil {
			errCh <- fmt.Sprintf("server read: %v", err)
			return
		}

		var req rpcEnvelope
		if err := json.Unmarshal(data, &req); err != nil {
			errCh <- fmt.Sprintf("server unmarshal: %v", err)
			return
		}

		// Inspect params for cwd and mcpServers.
		var params map[string]json.RawMessage
		if err := json.Unmarshal(req.Params, &params); err != nil {
			errCh <- fmt.Sprintf("unmarshal params: %v", err)
			return
		}
		if _, ok := params["cwd"]; !ok {
			errCh <- "params missing 'cwd'"
			return
		}
		if _, ok := params["mcpServers"]; !ok {
			errCh <- "params missing 'mcpServers'"
			return
		}

		resp := fmt.Sprintf(`{"jsonrpc":"2.0","result":{"sessionId":"20260708_1"},"id":%d}`, *req.ID)
		if err := conn.Write(ctx, websocket.MessageText, []byte(resp)); err != nil {
			errCh <- fmt.Sprintf("server write: %v", err)
			return
		}
		errCh <- "" // success
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	sessionID, err := client.NewSession(ctx, "/workspace/repo")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sessionID != "20260708_1" {
		t.Fatalf("expected session ID %q, got %q", "20260708_1", sessionID)
	}

	// Check for server-side errors.
	select {
	case errMsg := <-errCh:
		if errMsg != "" {
			t.Fatalf("server-side error: %s", errMsg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server handler to finish")
	}
}

// --- Test 6: Prompt blocks and returns stopReason and usage ---

func TestPrompt_BlocksAndReturnsStopReasonAndUsage(t *testing.T) {
	const serverDelay = 150 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}

		var req rpcEnvelope
		if err := json.Unmarshal(data, &req); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}

		// Simulate the agent taking time to complete its turn.
		time.Sleep(serverDelay)

		resp := fmt.Sprintf(`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"totalTokens":100,"inputTokens":80,"outputTokens":20}},"id":%d}`, *req.ID)
		if err := conn.Write(ctx, websocket.MessageText, []byte(resp)); err != nil {
			t.Errorf("server write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	start := time.Now()
	result, err := client.Prompt(ctx, "sess-1", "hello")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Prove it genuinely blocked (at least most of the server delay).
	if elapsed < 100*time.Millisecond {
		t.Fatalf("Prompt returned too quickly (%v), expected it to block for ~%v", elapsed, serverDelay)
	}

	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end_turn")
	}
	if result.Usage.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100", result.Usage.TotalTokens)
	}
	if result.Usage.InputTokens != 80 {
		t.Errorf("InputTokens = %d, want 80", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d, want 20", result.Usage.OutputTokens)
	}
}

// --- Test 7: Prompt returns an error on a JSON-RPC error response ---

func TestPrompt_ReturnsErrorOnRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}

		var req rpcEnvelope
		if err := json.Unmarshal(data, &req); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}

		resp := fmt.Sprintf(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params"},"id":%d}`, *req.ID)
		if err := conn.Write(ctx, websocket.MessageText, []byte(resp)); err != nil {
			t.Errorf("server write: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	_, err = client.Prompt(ctx, "sess-1", "hello")
	if err == nil {
		t.Fatal("expected Prompt to return an error on RPC error response")
	}
	if !strings.Contains(err.Error(), "Invalid params") && !strings.Contains(err.Error(), "-32602") {
		t.Fatalf("expected error to contain 'Invalid params' or '-32602', got: %v", err)
	}
}

// --- Test 8: Notifications receives server-initiated messages ---

func TestNotifications_ReceivesServerInitiatedMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()

		// Send a notification (no id field).
		notification := `{"jsonrpc":"2.0","method":"session/update","params":{"type":"message_chunk","text":"hello"}}`
		if err := conn.Write(ctx, websocket.MessageText, []byte(notification)); err != nil {
			t.Errorf("server write notification: %v", err)
			return
		}

		// Read until client closes so the close handshake completes promptly.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	select {
	case n := <-client.Notifications():
		if n.Method != "session/update" {
			t.Fatalf("notification Method = %q, want %q", n.Method, "session/update")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

// --- Test 9: Concurrent calls get correctly matched responses ---

func TestClient_ConcurrentCallsGetCorrectlyMatchedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()

		// Read 3 requests, then reply in reverse order.
		type incoming struct {
			ID     int64
			Method string
		}
		var requests []incoming
		for i := 0; i < 3; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("server read %d: %v", i, err)
				return
			}
			var req rpcEnvelope
			if err := json.Unmarshal(data, &req); err != nil {
				t.Errorf("server unmarshal %d: %v", i, err)
				return
			}
			requests = append(requests, incoming{ID: *req.ID, Method: req.Method})
		}

		// Sort by ID descending to reply in reverse order.
		sort.Slice(requests, func(i, j int) bool {
			return requests[i].ID > requests[j].ID
		})

		for _, req := range requests {
			// Encode the method in the result so the client can verify
			// it got the right response for its call.
			var resp string
			switch req.Method {
			case "initialize":
				resp = fmt.Sprintf(`{"jsonrpc":"2.0","result":{"method":"initialize"},"id":%d}`, req.ID)
			case "session/new":
				resp = fmt.Sprintf(`{"jsonrpc":"2.0","result":{"sessionId":"concurrent-test","method":"session/new"},"id":%d}`, req.ID)
			case "session/prompt":
				resp = fmt.Sprintf(`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"totalTokens":10,"inputTokens":7,"outputTokens":3},"method":"session/prompt"},"id":%d}`, req.ID)
			default:
				resp = fmt.Sprintf(`{"jsonrpc":"2.0","result":{"method":"%s"},"id":%d}`, req.Method, req.ID)
			}
			if err := conn.Write(ctx, websocket.MessageText, []byte(resp)); err != nil {
				t.Errorf("server write for id %d: %v", req.ID, err)
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Connect(ctx, server.URL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close(ctx)

	var wg sync.WaitGroup
	errs := make(chan error, 3)

	// Call 1: Initialize
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := client.Initialize(ctx); err != nil {
			errs <- fmt.Errorf("Initialize: %w", err)
		}
	}()

	// Call 2: NewSession
	wg.Add(1)
	go func() {
		defer wg.Done()
		sessionID, err := client.NewSession(ctx, "/workspace")
		if err != nil {
			errs <- fmt.Errorf("NewSession: %w", err)
			return
		}
		if sessionID != "concurrent-test" {
			errs <- fmt.Errorf("NewSession returned %q, want %q", sessionID, "concurrent-test")
		}
	}()

	// Call 3: Prompt
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := client.Prompt(ctx, "sess-1", "test")
		if err != nil {
			errs <- fmt.Errorf("Prompt: %w", err)
			return
		}
		if result.StopReason != "end_turn" {
			errs <- fmt.Errorf("Prompt StopReason = %q, want %q", result.StopReason, "end_turn")
		}
		if result.Usage.TotalTokens != 10 {
			errs <- fmt.Errorf("Prompt TotalTokens = %d, want 10", result.Usage.TotalTokens)
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
