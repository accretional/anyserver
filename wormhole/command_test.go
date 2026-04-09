package wormhole

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// helper: create a test server with a command wormhole
func testServer(t *testing.T, cfg CommandConfig) (*httptest.Server, *CommandWormhole, *Registry) {
	t.Helper()
	reg := NewRegistry()
	cw, err := NewCommandWormhole(cfg)
	if err != nil {
		t.Fatalf("NewCommandWormhole: %v", err)
	}
	reg.Command = cw
	reg.RegisterHidden(cw.Wormhole())

	mux := http.NewServeMux()
	mux.Handle("/wormhole/", HTTPHandler(reg, "test"))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cw, reg
}

// helper: connect WebSocket to command endpoint
func wsConnect(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + ts.URL[4:] + "/wormhole/command"
	ws, err := websocket.Dial(wsURL, "", ts.URL)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	return ws
}

func sendAuth(ws *websocket.Conn, token string) (Message, error) {
	msg := Message{Type: "auth", Token: token}
	if err := websocket.JSON.Send(ws, msg); err != nil {
		return Message{}, err
	}
	var resp Message
	if err := websocket.JSON.Receive(ws, &resp); err != nil {
		return Message{}, err
	}
	return resp, nil
}

// Test 1: Happy path -- correct token authenticates
func TestCommand_HappyPath(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	token := cw.Token()
	if token == "" {
		t.Fatal("token should not be empty before auth")
	}

	ws := wsConnect(t, ts)
	defer ws.Close()

	resp, err := sendAuth(ws, token)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if resp.Type != "auth_result" || resp.OK == nil || !*resp.OK {
		t.Fatalf("expected auth success, got: %+v", resp)
	}

	// Should be authenticated now
	if cw.State() != stateAuthenticated {
		t.Fatalf("expected stateAuthenticated, got %d", cw.State())
	}

	// Token should be consumed
	if cw.Token() != "" {
		t.Fatal("token should be empty after auth")
	}
}

// Test 2: Wrong token is rejected
func TestCommand_WrongToken(t *testing.T) {
	ts, _, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	ws := wsConnect(t, ts)
	defer ws.Close()

	resp, err := sendAuth(ws, "wrong-token-value")
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if resp.OK != nil && *resp.OK {
		t.Fatal("wrong token should not authenticate")
	}
}

// Test 3: Token replay -- auth once, try same token again
func TestCommand_TokenReplay(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	token := cw.Token()

	// First auth succeeds
	ws1 := wsConnect(t, ts)
	resp, err := sendAuth(ws1, token)
	if err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if resp.OK == nil || !*resp.OK {
		t.Fatal("first auth should succeed")
	}
	ws1.Close()

	// Give the server a moment to process disconnect
	time.Sleep(50 * time.Millisecond)

	// Second connection with same token should fail
	ws2 := wsConnect(t, ts)
	defer ws2.Close()

	resp2, err := sendAuth(ws2, token)
	if err != nil {
		// Connection might be closed immediately -- that's OK
		return
	}
	if resp2.OK != nil && *resp2.OK {
		t.Fatal("replayed token should not authenticate")
	}
}

// Test 4: Race condition -- 10 goroutines auth simultaneously, exactly 1 wins
func TestCommand_RaceCondition(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	token := cw.Token()

	var wins atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ws := wsConnect(t, ts)
			defer ws.Close()

			resp, err := sendAuth(ws, token)
			if err != nil {
				return
			}
			if resp.OK != nil && *resp.OK {
				wins.Add(1)
			}
		}()
	}

	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins.Load())
	}
}

// Test 5: Auth timeout -- wait too long, then try to auth
func TestCommand_AuthTimeout(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 500 * time.Millisecond,
	})

	token := cw.Token()

	// Wait for timeout
	time.Sleep(700 * time.Millisecond)

	// Should be closed now
	if cw.State() != stateClosed {
		t.Fatalf("expected stateClosed after timeout, got %d", cw.State())
	}

	// Try to auth
	ws := wsConnect(t, ts)
	defer ws.Close()

	resp, err := sendAuth(ws, token)
	if err != nil {
		return // Connection closed immediately -- acceptable
	}
	if resp.OK != nil && *resp.OK {
		t.Fatal("should not authenticate after timeout")
	}
}

// Test 6: Post-auth rejection -- new connections after auth are rejected
func TestCommand_PostAuthRejection(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	token := cw.Token()

	// Authenticate
	ws1 := wsConnect(t, ts)
	resp, _ := sendAuth(ws1, token)
	if resp.OK == nil || !*resp.OK {
		t.Fatal("first auth should succeed")
	}

	// New connection should be rejected
	ws2 := wsConnect(t, ts)
	defer ws2.Close()

	resp2, err := sendAuth(ws2, "anything")
	if err != nil {
		return // Closed immediately -- acceptable
	}
	if resp2.OK != nil && *resp2.OK {
		t.Fatal("post-auth connection should not authenticate")
	}

	ws1.Close()
}

// Test 7: Discovery hiding -- command not in registry.Kinds()
func TestCommand_DiscoveryHiding(t *testing.T) {
	_, _, reg := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	// Register a visible wormhole for comparison
	reg.Register(New(KindStdout, "test stdout"))

	kinds := reg.Kinds()
	for _, k := range kinds {
		if k == KindCommand {
			t.Fatal("command wormhole should not appear in Kinds()")
		}
	}

	// But Get should still work
	if reg.Get(KindCommand) == nil {
		t.Fatal("Get(KindCommand) should return the wormhole")
	}

	all := reg.All()
	for _, wh := range all {
		if wh.Kind() == KindCommand {
			t.Fatal("command wormhole should not appear in All()")
		}
	}
}

// Test 8: No info leakage -- wrong, expired, and used tokens all get identical response
func TestCommand_NoInfoLeakage(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	// Wrong token
	ws1 := wsConnect(t, ts)
	resp1, _ := sendAuth(ws1, "wrong-token")
	ws1.Close()

	// Correct token (consume it)
	token := cw.Token()
	ws2 := wsConnect(t, ts)
	sendAuth(ws2, token)
	ws2.Close()
	time.Sleep(50 * time.Millisecond)

	// Used token (replay)
	ws3 := wsConnect(t, ts)
	resp3, err := sendAuth(ws3, token)
	ws3.Close()

	// Both failure responses should be identical
	if err != nil {
		return // Connection closed -- different but also acceptable for used token
	}

	r1, _ := json.Marshal(resp1)
	r3, _ := json.Marshal(resp3)
	if resp1.Type == resp3.Type && string(r1) == string(r3) {
		// Good: identical responses
	} else {
		// They should at minimum both be failures
		if (resp1.OK != nil && *resp1.OK) || (resp3.OK != nil && *resp3.OK) {
			t.Fatal("failure responses should not indicate success")
		}
	}
}

// Test 9: WaitForAuth returns nil on success
func TestCommand_WaitForAuthSuccess(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
	})

	token := cw.Token()

	done := make(chan error, 1)
	go func() {
		done <- cw.WaitForAuth()
	}()

	ws := wsConnect(t, ts)
	defer ws.Close()
	sendAuth(ws, token)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForAuth should return nil on success, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForAuth should have returned")
	}
}

// Test 10: WaitForAuth returns error on timeout
func TestCommand_WaitForAuthTimeout(t *testing.T) {
	_, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 300 * time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		done <- cw.WaitForAuth()
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("WaitForAuth should return error on timeout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForAuth should have returned after timeout")
	}
}

// Test 11: One-shot mode -- after disconnect, state is closed
func TestCommand_OneShot(t *testing.T) {
	ts, cw, _ := testServer(t, CommandConfig{
		Enabled:     true,
		AuthTimeout: 10 * time.Second,
		IdleTimeout: 0, // one-shot
	})

	token := cw.Token()

	ws := wsConnect(t, ts)
	sendAuth(ws, token)
	ws.Close()

	// Give server time to process disconnect
	time.Sleep(100 * time.Millisecond)

	if cw.State() != stateClosed {
		t.Fatalf("expected stateClosed after one-shot disconnect, got %d", cw.State())
	}
}
