package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Server is the daemon control plane. Mirrors reference-daemon-code-agent-js/
// supervisor.js (single-instance lock, status file, lifecycle) + the accept loop
// that routes framed control requests to the SessionManager/Pool. It listens on
// an owner-only local Unix socket and NEVER binds a TCP port.
type Server struct {
	opts      ServerOptions
	startedAt time.Time

	mu       sync.Mutex // guards listener + conns
	listener net.Listener
	conns    map[net.Conn]struct{} // open connections, closed on Shutdown so blocked reads return
	lock     *fileLock

	ctx    context.Context
	cancel context.CancelFunc

	wg           sync.WaitGroup
	shutdownOnce sync.Once
	done         chan struct{}
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Paths   Paths
	Manager *SessionManager
	Pool    *Pool
	Version int
	Now     func() time.Time
	Log     func(string)
	isAlive func(int) bool // test hook for the single-instance lock
}

// NewServer validates options and builds a Server.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Manager == nil || opts.Pool == nil {
		return nil, errors.New("daemon: server requires a Pool and SessionManager")
	}
	if opts.Paths.Socket == "" || opts.Paths.Lock == "" || opts.Paths.Status == "" {
		return nil, errors.New("daemon: server requires socket, lock, and status paths")
	}
	if opts.Version <= 0 {
		opts.Version = ProtoVersion
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		opts:   opts,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		conns:  map[net.Conn]struct{}{},
	}, nil
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(fmt.Sprintf(format, args...))
	}
}

// Serve acquires the single-instance lock, binds the owner-only control socket,
// writes the status file, and serves connections until Shutdown. It blocks. On
// return it has released the lock and removed the socket/status files.
func (s *Server) Serve() error {
	if err := checkSocketPathLength(s.opts.Paths.Socket); err != nil {
		return err
	}
	if err := secureSocketParent(s.opts.Paths.Socket); err != nil {
		return err
	}
	lock, err := acquireLock(s.opts.Paths.Lock, s.opts.isAlive)
	if err != nil {
		return err
	}
	s.lock = lock
	defer s.cleanup()

	// A leftover socket file from an unclean exit would make Listen fail with
	// "address already in use"; we hold the lock, so any socket here is stale.
	_ = os.Remove(s.opts.Paths.Socket)

	listener, err := net.Listen("unix", s.opts.Paths.Socket)
	if err != nil {
		return fmt.Errorf("daemon: bind control socket: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	// If Shutdown already fired during the bind window, close now and bail so a
	// shutdown requested at startup is never lost (the accept loop would otherwise
	// block forever waiting for a connection that never comes) (D4).
	select {
	case <-s.done:
		_ = listener.Close()
		s.wg.Wait()
		return nil
	default:
	}
	if err := hardenSocketFile(s.opts.Paths.Socket); err != nil {
		return fmt.Errorf("daemon: harden control socket: %w", err)
	}
	s.startedAt = s.opts.Now()
	if err := s.writeStatusFile(); err != nil {
		return err
	}
	s.logf("daemon listening on %s (pid %d)", s.opts.Paths.Socket, os.Getpid())

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				s.wg.Wait()
				return nil // clean shutdown
			default:
				// Transient accept error during normal operation.
				s.logf("accept error: %v", err)
				return fmt.Errorf("daemon: accept: %w", err)
			}
		}
		s.trackConn(conn)
		s.wg.Go(func() {
			defer s.untrackConn(conn)
			s.handleConn(conn)
		})
	}
}

// trackConn registers an accepted connection so Shutdown can close it. If a
// shutdown is already in progress it closes the connection immediately rather
// than serving it.
func (s *Server) trackConn(c net.Conn) {
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		_ = c.Close()
		return
	default:
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrackConn(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// Shutdown stops accepting connections, cancels in-flight runs, drains the pool,
// and removes the socket/lock/status files. Safe to call multiple times.
func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() {
		close(s.done)
		s.cancel() // stop in-flight pool runs
		s.mu.Lock()
		if s.listener != nil {
			_ = s.listener.Close()
		}
		// Close every open connection so a handler blocked on an idle/hostile
		// client's read returns at once and wg.Wait() can finish — otherwise a single
		// stalled connection wedges shutdown (and SIGTERM cleanup) forever (D3).
		for c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.opts.Pool.Drain()
	})
}

func (s *Server) cleanup() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	_ = os.Remove(s.opts.Paths.Socket)
	_ = os.Remove(s.opts.Paths.Status)
	if s.lock != nil {
		_ = s.lock.release()
	}
}

func (s *Server) writeStatusFile() error {
	status := StatusFile{
		PID:       os.Getpid(),
		Socket:    s.opts.Paths.Socket,
		Version:   s.opts.Version,
		StartedAt: s.startedAt,
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.opts.Paths.Status, data, 0o600); err != nil {
		return fmt.Errorf("daemon: write status file: %w", err)
	}
	return nil
}

// ServeConn runs the control protocol (handshake + one command) on an
// already-established connection, reusing the exact local dispatch path. The
// remote bridge calls it AFTER authenticating a TLS connection, so a remote
// session is handled identically to a local one (same SessionManager/Pool, same
// sandbox/risk model) — remote never bypasses the local controls. It closes conn.
func (s *Server) ServeConn(conn net.Conn) {
	// Track the conn so Shutdown can close it: the remote bridge enters here (not via
	// the local accept loop), so without this a remote connection stalled in the
	// pre-stream handshake read would survive Shutdown's conns-close sweep. (AUDIT-M8)
	s.trackConn(conn)
	defer s.untrackConn(conn)
	s.handleConn(conn)
}

// handleConn performs the handshake then dispatches a single control command.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	// Bound the handshake (hello + first command). The remote bridge clears the conn
	// deadline before handing off and the local socket sets none, so an idle/hostile
	// peer that connects but never completes the exchange would otherwise pin a
	// handler goroutine (and, on the bridge, a connection slot) forever. Cleared once
	// the command is read, before the (long-lived) streaming phase. (AUDIT-M7, AUDIT-I1)
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	hello, err := ReadControl(conn)
	if err != nil {
		return
	}
	if hello.Type != CtrlHello {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: "expected hello"})
		return
	}
	version, ok := NegotiateVersion(hello.Version)
	if !ok {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: "unsupported protocol version"})
		return
	}
	if err := WriteControl(conn, Ctrl{Type: CtrlHelloOK, Version: version}); err != nil {
		return
	}

	cmd, err := ReadControl(conn)
	if err != nil {
		return
	}
	// Handshake complete — the dispatched command may stream indefinitely, so drop
	// the deadline before handing off.
	_ = conn.SetDeadline(time.Time{})
	switch cmd.Type {
	case CtrlRun:
		s.handleRun(conn, cmd)
	case CtrlAttach:
		s.handleAttach(conn, cmd)
	case CtrlStatus:
		s.handleStatus(conn)
	case CtrlShutdown:
		_ = WriteControl(conn, Ctrl{Type: CtrlAck, Message: "shutting down"})
		s.Shutdown()
	default:
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: fmt.Sprintf("unknown command %q", cmd.Type)})
	}
}

func (s *Server) handleRun(conn net.Conn, cmd Ctrl) {
	if cmd.Session == "" {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: "run requires a session id"})
		return
	}
	args := cmd.Args
	if cmd.Prompt != "" {
		args = append(args, "--prompt", cmd.Prompt)
	}
	sess, err := s.opts.Manager.Start(s.ctx, WorkerSpec{Session: cmd.Session, Cwd: cmd.Cwd, Args: args})
	if err != nil {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: err.Error()})
		return
	}
	_ = WriteControl(conn, Ctrl{Type: CtrlAck, Session: sess.ID()})
	buffered, live, cancel := sess.Subscribe()
	defer cancel()
	s.streamToClient(conn, sess, buffered, live)
}

func (s *Server) handleAttach(conn net.Conn, cmd Ctrl) {
	if cmd.Session == "" {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: "attach requires a session id"})
		return
	}
	buffered, live, cancel, err := s.opts.Manager.Attach(cmd.Session)
	if err != nil {
		_ = WriteControl(conn, Ctrl{Type: CtrlError, Message: err.Error()})
		return
	}
	defer cancel()
	sess, _ := s.opts.Manager.Get(cmd.Session)
	_ = WriteControl(conn, Ctrl{Type: CtrlAck, Session: cmd.Session})
	s.streamToClient(conn, sess, buffered, live)
}

// streamToClient writes the buffered history then live lines as CtrlData frames,
// finishing with CtrlEnd (or CtrlError if the session failed). A write error
// (client disconnected) ends the stream without affecting the session.
func (s *Server) streamToClient(conn net.Conn, sess *Session, buffered []string, live <-chan string) {
	for _, line := range buffered {
		// Honor shutdown during history replay too: a large buffer would otherwise
		// keep writing after Shutdown, delaying drain (D5).
		select {
		case <-s.done:
			_ = WriteControl(conn, Ctrl{Type: CtrlEnd, Message: "daemon shutting down"})
			return
		default:
		}
		if err := WriteControl(conn, Ctrl{Type: CtrlData, Line: line}); err != nil {
			return
		}
	}
	for {
		select {
		case line, ok := <-live:
			if !ok {
				s.finishStream(conn, sess)
				return
			}
			if err := WriteControl(conn, Ctrl{Type: CtrlData, Line: line}); err != nil {
				return
			}
		case <-s.done:
			_ = WriteControl(conn, Ctrl{Type: CtrlEnd, Message: "daemon shutting down"})
			return
		}
	}
}

func (s *Server) finishStream(conn net.Conn, sess *Session) {
	if sess != nil {
		if err := sess.Err(); err != nil {
			_ = WriteControl(conn, Ctrl{Type: CtrlError, Session: sess.ID(), Message: err.Error()})
			return
		}
	}
	_ = WriteControl(conn, Ctrl{Type: CtrlEnd})
}

func (s *Server) handleStatus(conn net.Conn) {
	report := StatusReport{
		PID:        os.Getpid(),
		Version:    s.opts.Version,
		Socket:     s.opts.Paths.Socket,
		StartedAt:  s.startedAt,
		PoolSize:   s.opts.Pool.Size(),
		Workers:    s.opts.Pool.WorkerStats(),
		Sessions:   s.opts.Manager.Statuses(),
		QueueDepth: s.opts.Pool.QueueDepth(),
	}
	_ = WriteControl(conn, Ctrl{Type: CtrlStatusResult, Status: &report})
}
