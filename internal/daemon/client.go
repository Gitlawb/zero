package daemon

import (
	"fmt"
	"net"
	"time"
)

// Client is a control-socket client used by the `zero daemon run|attach|status|
// stop` subcommands. It performs the version handshake on Dial.
type Client struct {
	conn net.Conn
}

// Dial connects to the daemon control socket and completes the handshake.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn}
	if err := c.handshake(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake() error {
	if err := WriteControl(c.conn, Ctrl{Type: CtrlHello, Version: ProtoVersion}); err != nil {
		return err
	}
	reply, err := ReadControl(c.conn)
	if err != nil {
		return err
	}
	if reply.Type != CtrlHelloOK {
		return fmt.Errorf("daemon: handshake rejected: %s", reply.Message)
	}
	return nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Run starts/routes a session and streams its stream-json output lines to onLine
// until the session ends. It returns the session's terminal error, if any.
func (c *Client) Run(session, cwd, prompt string, args []string, onLine func(string)) error {
	if err := WriteControl(c.conn, Ctrl{Type: CtrlRun, Session: session, Cwd: cwd, Prompt: prompt, Args: args}); err != nil {
		return err
	}
	return c.streamLoop(onLine)
}

// Attach streams a running session's output (buffered history + live) to onLine.
func (c *Client) Attach(session string, onLine func(string)) error {
	if err := WriteControl(c.conn, Ctrl{Type: CtrlAttach, Session: session}); err != nil {
		return err
	}
	return c.streamLoop(onLine)
}

func (c *Client) streamLoop(onLine func(string)) error {
	for {
		msg, err := ReadControl(c.conn)
		if err != nil {
			return err
		}
		switch msg.Type {
		case CtrlAck:
			// dispatch acknowledged; keep reading
		case CtrlData:
			if onLine != nil {
				onLine(msg.Line)
			}
		case CtrlEnd:
			return nil
		case CtrlError:
			return fmt.Errorf("daemon: %s", msg.Message)
		default:
			return fmt.Errorf("daemon: unexpected message %q", msg.Type)
		}
	}
}

// Status requests the daemon/worker/session status report.
func (c *Client) Status() (*StatusReport, error) {
	if err := WriteControl(c.conn, Ctrl{Type: CtrlStatus}); err != nil {
		return nil, err
	}
	msg, err := ReadControl(c.conn)
	if err != nil {
		return nil, err
	}
	if msg.Type == CtrlError {
		return nil, fmt.Errorf("daemon: %s", msg.Message)
	}
	if msg.Type != CtrlStatusResult || msg.Status == nil {
		return nil, fmt.Errorf("daemon: unexpected status reply %q", msg.Type)
	}
	return msg.Status, nil
}

// Shutdown asks the daemon to drain and stop.
func (c *Client) Shutdown() error {
	if err := WriteControl(c.conn, Ctrl{Type: CtrlShutdown}); err != nil {
		return err
	}
	msg, err := ReadControl(c.conn)
	if err != nil {
		return err
	}
	if msg.Type == CtrlError {
		return fmt.Errorf("daemon: %s", msg.Message)
	}
	return nil
}
