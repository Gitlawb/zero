package daemon

import "time"

// StatusReport is the daemon/worker/session snapshot returned by `zero daemon
// status` (CtrlStatusResult). Mirrors reference-daemon-code-agent-js/status.js.
type StatusReport struct {
	PID        int             `json:"pid"`
	Version    int             `json:"version"`
	Socket     string          `json:"socket"`
	StartedAt  time.Time       `json:"startedAt"`
	PoolSize   int             `json:"poolSize"`
	Workers    []WorkerStatus  `json:"workers"`
	Sessions   []SessionStatus `json:"sessions"`
	QueueDepth int             `json:"queueDepth"`
}

// WorkerStatus is one worker's lifecycle snapshot.
type WorkerStatus struct {
	ID                 int    `json:"id"`
	PID                int    `json:"pid"`
	State              string `json:"state"`
	ConsecutiveCrashes int    `json:"consecutiveCrashes"`
	Restarts           int    `json:"restarts"`
	Session            string `json:"session,omitempty"`
}

// SessionStatus is one session's routing/metrics snapshot.
type SessionStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Lines int    `json:"lines"`
}

// StatusFile is the on-disk daemon status document (pid, socket, version,
// started-at), written next to the lock so `status`/`stop` work without a live
// connection. Mirrors supervisor.js's status file.
type StatusFile struct {
	PID       int       `json:"pid"`
	Socket    string    `json:"socket"`
	Version   int       `json:"version"`
	StartedAt time.Time `json:"startedAt"`
}
