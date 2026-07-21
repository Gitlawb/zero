package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClients() *ToolHTTP {
	cfg := HTTPClientConfig{
		DialTimeout:         200 * time.Millisecond,
		TLSHandshakeTimeout: 200 * time.Millisecond,
		StreamIdleTimeout:   100 * time.Millisecond,
		DefaultExecTimeout:  100 * time.Millisecond,
	}
	return NewToolHTTP(NewHTTPClients(cfg))
}

func TestStreamingHeaderDelayUnbounded(t *testing.T) {
	// Server delays headers then streams data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // header delay > default exec timeout
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: 1\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(w, "data: 2\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	tool := newTestClients()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx := context.Background()
	resp, err := tool.Do(ctx, req, ToolCallStreaming, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	// Read first event to confirm stream success after delayed headers.
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read first line: %v", err)
	}
	if line == "" {
		t.Fatalf("empty first line")
	}
}

func TestFiniteRespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tool := newTestClients()
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	ctx := context.Background()
	custom := 50 * time.Millisecond
	_, err := tool.Do(ctx, req, ToolCallFinite, &custom)
	if err == nil {
		t.Fatalf("expected deadline exceeded, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got: %v", err)
	}
}

func TestStreamingIdleWatchdogCancels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: hello\n\n")
		if fl != nil {
			fl.Flush()
		}
		// Stall longer than idle timeout to trigger watchdog.
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	tool := newTestClients()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx := context.Background()
	resp, err := tool.Do(ctx, req, ToolCallStreaming, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	// Read first event
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("failed reading first line: %v", err)
	}
	start := time.Now()
	// Next read should error due to idle watchdog cancel.
	_, err = reader.ReadString('\n')
	if err == nil {
		t.Fatalf("expected read error after idle timeout")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("idle watchdog did not cancel in time")
	}
}
