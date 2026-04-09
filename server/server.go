// Package server implements a dual gRPC/HTTP server on a single port via h2c,
// with grpc-gateway proxy and request counting middleware.
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	appmetrics "github.com/accretional/anyserver/metrics"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

// RegisterFunc registers gRPC services on a server.
type RegisterFunc func(s *grpc.Server)

// GatewayRegisterFunc registers grpc-gateway handlers on a mux.
type GatewayRegisterFunc func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error

// BootGate controls access to routes during the boot handshake.
// While the gate is not ready, only paths for which AllowPath returns true
// are served; all others receive 503.
type BootGate struct {
	Ready     chan struct{}            // closed when boot completes
	AllowPath func(path string) bool  // returns true for paths allowed before boot
}

// Config holds server configuration.
type Config struct {
	Port             int
	GRPCRegister     RegisterFunc
	GatewayRegisters []GatewayRegisterFunc
	HTTPMux          *http.ServeMux // additional HTTP routes
	RequestCounter   *appmetrics.RequestCounter
	Gate             *BootGate      // if set, gates non-allowed routes until Ready
	Listening        chan struct{}   // if set, closed after net.Listen succeeds
}

// Run starts a dual gRPC/HTTP server on a single port.
func Run(cfg Config) error {
	grpcServer := grpc.NewServer(
		grpc.MaxSendMsgSize(32 * 1024 * 1024),
		grpc.MaxRecvMsgSize(32 * 1024 * 1024),
	)

	cfg.GRPCRegister(grpcServer)
	reflection.Register(grpcServer)

	// Set up grpc-gateway
	ctx := context.Background()
	gwMux := runtime.NewServeMux()
	addr := fmt.Sprintf("localhost:%d", cfg.Port)
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(32*1024*1024),
		),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	for _, reg := range cfg.GatewayRegisters {
		if err := reg(ctx, gwMux, conn); err != nil {
			return fmt.Errorf("gateway register: %w", err)
		}
	}

	// HTTP mux: merge user routes, gateway, and fallback
	httpMux := http.NewServeMux()
	if cfg.HTTPMux != nil {
		httpMux = cfg.HTTPMux
	}

	// Gateway routes under /gateway/ prefix (raw grpc-gateway proxy)
	httpMux.Handle("/gateway/", http.StripPrefix("/gateway", gwMux))

	// Wrap HTTP with boot gate if provided
	var httpHandler http.Handler = httpMux
	if cfg.Gate != nil {
		gate := cfg.Gate
		httpHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-gate.Ready:
				// Gate open, serve normally
			default:
				// Gate closed: check if this path is allowed through
				if gate.AllowPath == nil || !gate.AllowPath(r.URL.Path) {
					w.Header().Set("Retry-After", "5")
					http.Error(w, "server is booting", http.StatusServiceUnavailable)
					return
				}
			}
			httpMux.ServeHTTP(w, r)
		})
	}

	// Wrap HTTP with request counter if provided
	if cfg.RequestCounter != nil {
		httpHandler = cfg.RequestCounter.Wrap(httpHandler)
	}

	// Dual handler: route gRPC vs HTTP based on content-type
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
		} else {
			httpHandler.ServeHTTP(w, r)
		}
	})

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	httpServer := &http.Server{
		Handler: h2c.NewHandler(handler, &http2.Server{}),
	}

	log.Printf("anyserver listening on http://localhost:%d", cfg.Port)
	if cfg.Listening != nil {
		close(cfg.Listening)
	}
	return httpServer.Serve(lis)
}
