package wormhole

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

const KindCommand Kind = "command"

// CommandConfig configures the command wormhole boot handshake.
type CommandConfig struct {
	// Enabled activates the command wormhole.
	Enabled bool

	// AuthTimeout is how long the server waits for a client to authenticate
	// before proceeding without a command session. Default 60s.
	AuthTimeout time.Duration

	// IdleTimeout is how long to wait after the authenticated client
	// disconnects before shutting down. Zero means one-shot (no reconnect,
	// no shutdown -- command wormhole just closes permanently).
	IdleTimeout time.Duration

	// ShutdownFunc is called when idle timeout expires after client disconnect.
	// If nil, os.Exit(0) is used.
	ShutdownFunc func()
}

type commandState int

const (
	stateAwaitingAuth commandState = iota
	stateAuthenticated
	stateClosed
)

// CommandWormhole manages the authenticated bidirectional command channel.
type CommandWormhole struct {
	mu       sync.Mutex
	config   CommandConfig
	token    []byte // crypto/rand generated, zeroed after auth
	state    commandState
	authDone chan struct{} // closed when auth succeeds or times out
	authErr  error        // non-nil if auth timed out
	wh       *Wormhole    // underlying broadcast wormhole
}

// NewCommandWormhole creates a command wormhole, generates a token, and prints
// it to stderr. The caller should then call WaitForAuth to block until a client
// authenticates or the timeout expires.
func NewCommandWormhole(cfg CommandConfig) (*CommandWormhole, error) {
	if cfg.AuthTimeout == 0 {
		cfg.AuthTimeout = 60 * time.Second
	}
	if cfg.AuthTimeout > 5*time.Minute {
		cfg.AuthTimeout = 5 * time.Minute
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	token := []byte(hex.EncodeToString(tokenBytes))

	cw := &CommandWormhole{
		config:   cfg,
		token:    token,
		state:    stateAwaitingAuth,
		authDone: make(chan struct{}),
		wh:       New(KindCommand, "Authenticated command channel"),
	}

	// Start auth timeout
	time.AfterFunc(cfg.AuthTimeout, func() {
		cw.mu.Lock()
		defer cw.mu.Unlock()
		if cw.state == stateAwaitingAuth {
			cw.state = stateClosed
			cw.authErr = errors.New("command auth timed out")
			cw.zeroToken()
			close(cw.authDone)
		}
	})

	return cw, nil
}

// Token returns the current token for testing. Returns empty if consumed.
func (cw *CommandWormhole) Token() string {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return string(cw.token)
}

// Authenticate attempts to verify the given token. Returns true if the caller
// is now the authenticated command client. Thread-safe; exactly one caller wins.
func (cw *CommandWormhole) Authenticate(candidate string) bool {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.state != stateAwaitingAuth {
		return false
	}

	if len(cw.token) == 0 {
		return false
	}

	if subtle.ConstantTimeCompare([]byte(candidate), cw.token) != 1 {
		return false
	}

	// Success: consume token, transition state
	cw.state = stateAuthenticated
	cw.zeroToken()
	close(cw.authDone)
	return true
}

// WaitForAuth blocks until authentication succeeds or times out.
// Returns nil on success, error on timeout.
func (cw *CommandWormhole) WaitForAuth() error {
	<-cw.authDone
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.authErr
}

// State returns the current state. Used by the handler to decide behavior.
func (cw *CommandWormhole) State() commandState {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.state
}

// Wormhole returns the underlying broadcast wormhole.
func (cw *CommandWormhole) Wormhole() *Wormhole {
	return cw.wh
}

// OnClientDisconnect is called when the authenticated client disconnects.
// If IdleTimeout > 0, starts the idle timer. If one-shot, closes permanently.
func (cw *CommandWormhole) OnClientDisconnect() {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.config.IdleTimeout > 0 {
		time.AfterFunc(cw.config.IdleTimeout, func() {
			cw.mu.Lock()
			if cw.state != stateAuthenticated {
				cw.mu.Unlock()
				return
			}
			cw.state = stateClosed
			cw.mu.Unlock()

			shutdown := cw.config.ShutdownFunc
			if shutdown == nil {
				os.Exit(0)
			}
			shutdown()
		})
		// Allow re-auth window (transition back to awaiting but with no token,
		// so only idle timeout fires -- no new auth possible)
		return
	}

	// One-shot: close permanently
	cw.state = stateClosed
}

func (cw *CommandWormhole) zeroToken() {
	for i := range cw.token {
		cw.token[i] = 0
	}
	cw.token = nil
}
