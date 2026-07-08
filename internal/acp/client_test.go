package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_WaitReady_SucceedsOnceServerResponds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	if err := client.WaitReady(2 * time.Second); err != nil {
		t.Fatalf("WaitReady failed: %v", err)
	}
}

func TestClient_WaitReady_TimesOutIfServerNeverResponds(t *testing.T) {
	client := New("http://127.0.0.1:1") // nothing listens here
	err := client.WaitReady(300 * time.Millisecond)
	if err == nil {
		t.Fatal("expected WaitReady to time out and return an error")
	}
}

func TestClient_NewSessionAndPrompt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		switch req.Method {
		case "session/new":
			json.NewEncoder(w).Encode(newSessionResult{SessionID: "sess-1"})
		case "session/prompt":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	sessionID, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("expected session id 'sess-1', got %q", sessionID)
	}
	if err := client.Prompt(sessionID, "hello"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
}

func TestClient_NewSession_ErrorsOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL)
	if _, err := client.NewSession(); err == nil {
		t.Fatal("expected an error on 500 response")
	}
}

func TestClient_Stream_StopsAtTerminalEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		events := []string{
			`{"type":"usage","data":{"input_tokens":100,"output_tokens":20}}`,
			`{"type":"usage","data":{"input_tokens":50,"output_tokens":10}}`,
			`{"type":"complete"}`,
		}
		for _, e := range events {
			fmt.Fprintln(w, e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := client.Stream(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var seen []Event
	for e := range events {
		seen = append(seen, e)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 events, got %d", len(seen))
	}
	if !IsTerminal(seen[len(seen)-1]) {
		t.Fatalf("expected last event to be terminal, got %+v", seen[len(seen)-1])
	}
	if IsTerminal(seen[0]) {
		t.Fatalf("expected first event to NOT be terminal, got %+v", seen[0])
	}
}

func TestClient_Stream_ClosesChannelWhenContextCancelled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		fmt.Fprintln(w, `{"type":"usage","data":{"input_tokens":1,"output_tokens":1}}`)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done() // hang until the client disconnects
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithCancel(context.Background())

	events, err := client.Stream(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	<-events // consume the one event
	cancel()

	// The channel must close (not hang) once the context is cancelled.
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected channel to be closed after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close after cancellation")
	}
}

func TestClient_Stream_EmitsStreamErrorEventOnConnectionFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"type":"usage","data":{"input_tokens":1,"output_tokens":1}}`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected ResponseWriter to support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		conn.Close()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := client.Stream(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var seen []Event
	for e := range events {
		seen = append(seen, e)
	}

	if len(seen) < 2 {
		t.Fatalf("expected at least 2 events (usage + stream_error), got %d: %+v", len(seen), seen)
	}
	last := seen[len(seen)-1]
	if last.Type != "stream_error" {
		t.Fatalf("expected last event to be stream_error, got %+v", last)
	}
}

func TestUsageFromEvent_ExtractsTokenCounts(t *testing.T) {
	e := Event{Type: "usage", Data: json.RawMessage(`{"input_tokens":100,"output_tokens":20}`)}
	input, output := UsageFromEvent(e)
	if input != 100 || output != 20 {
		t.Errorf("expected (100, 20), got (%d, %d)", input, output)
	}
}

func TestUsageFromEvent_ReturnsZeroForNonUsageEvent(t *testing.T) {
	e := Event{Type: "complete"}
	input, output := UsageFromEvent(e)
	if input != 0 || output != 0 {
		t.Errorf("expected (0, 0) for non-usage event, got (%d, %d)", input, output)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		eventType string
		want      bool
	}{
		{"complete", true},
		{"failed", true},
		{"usage", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsTerminal(Event{Type: c.eventType}); got != c.want {
			t.Errorf("IsTerminal(%q) = %v, want %v", c.eventType, got, c.want)
		}
	}
}
