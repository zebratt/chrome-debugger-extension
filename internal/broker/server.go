package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"chrome-connector/internal/policy"
	"chrome-connector/internal/protocol"
	runtimepath "chrome-connector/internal/runtime"
)

const ProtocolVersion = "1.0"

type Server struct {
	policy        policy.Policy
	idleTimeout   time.Duration
	leases        *LeaseStore
	started       time.Time
	mutex         sync.Mutex
	hosts         map[string]*hostSession
	registered    chan struct{}
	registerOnce  sync.Once
	sequence      uint64
	activeClients int
	lastActivity  time.Time
}

func NewServer(activePolicy policy.Policy, idleTimeout time.Duration) *Server {
	return &Server{
		policy:       activePolicy,
		idleTimeout:  idleTimeout,
		leases:       NewLeaseStore(30*time.Second, time.Now),
		started:      time.Now(),
		hosts:        make(map[string]*hostSession),
		registered:   make(chan struct{}),
		lastActivity: time.Now(),
	}
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	if server.idleTimeout > 0 {
		go server.watchIdle(ctx, listener)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go server.handleConnection(conn)
	}
}

func (server *Server) watchIdle(ctx context.Context, listener net.Listener) {
	interval := server.idleTimeout / 4
	if interval > 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			server.mutex.Lock()
			idle := server.activeClients == 0 && time.Since(server.lastActivity) >= server.idleTimeout
			server.mutex.Unlock()
			if idle && server.leases.ActiveCount() == 0 {
				listener.Close()
				return
			}
		}
	}
}

func (server *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return
	}
	uid, err := runtimepath.PeerUID(unixConn)
	if err != nil || uid != uint32(os.Getuid()) {
		return
	}
	decoder := json.NewDecoder(conn)
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return
	}
	var hello hostHello
	if json.Unmarshal(first, &hello) == nil && hello.Kind == "hello" {
		server.handleHost(conn, decoder, hello)
		return
	}
	server.handleClient(conn, decoder, first)
}

func successResponse(id json.RawMessage, result any) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: result}
}
