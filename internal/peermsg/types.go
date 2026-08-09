package peermsg

import "time"

const (
	ProtocolVersion = 1
	maxMessageBytes = 64 * 1024
	maxFrameBytes   = 128 * 1024
)

type PermissionClass string

const (
	PermissionPrompting PermissionClass = "prompting"
	PermissionBypass    PermissionClass = "bypass"
)

type DeliveryStatus string

const (
	DeliveryAccepted  DeliveryStatus = "accepted"
	DeliveryHeld      DeliveryStatus = "held"
	DeliveryRefused   DeliveryStatus = "refused"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryDenied    DeliveryStatus = "denied"
	DeliveryExpired   DeliveryStatus = "expired"
)

type InboundPolicy string

const (
	InboundPolicyParity InboundPolicy = ""
	InboundPolicyAccept InboundPolicy = "accept"
	InboundPolicyHold   InboundPolicy = "hold"
	InboundPolicyRefuse InboundPolicy = "refuse"
)

type HoldCause string

const (
	HoldCauseModeMismatch HoldCause = "mode-mismatch"
	HoldCauseExplicit     HoldCause = "explicit-setting"
	HoldCauseModeUnknown  HoldCause = "mode-unknown"
)

// Identity is the model-independent identity published by one live Zero
// process. SessionID and Name are updated when the TUI creates, resumes, or
// renames its active session.
type Identity struct {
	SessionID       string          `json:"sessionId,omitempty"`
	Name            string          `json:"name,omitempty"`
	Cwd             string          `json:"cwd,omitempty"`
	PermissionClass PermissionClass `json:"permissionClass"`
}

// Peer is one reachable local Zero session.
type Peer struct {
	Identity
	Endpoint  string    `json:"endpoint"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Ref       string    `json:"ref"`
}

// InboundMessage is delivered to the receiving application. RequiringApproval
// is true when sender and receiver permission classes differ.
type InboundMessage struct {
	ID               string
	From             Peer
	Body             string
	Summary          string
	ReceivedAt       time.Time
	RequiresApproval bool
	HoldCause        HoldCause
	HopChain         []string
}

type sendFrame struct {
	Version  int            `json:"version"`
	Type     string         `json:"type"`
	ID       string         `json:"id"`
	From     Peer           `json:"from"`
	To       string         `json:"to"`
	Summary  string         `json:"summary"`
	Body     string         `json:"body"`
	HopChain []string       `json:"hopChain,omitempty"`
	OrigID   string         `json:"origMessageId,omitempty"`
	Status   DeliveryStatus `json:"status,omitempty"`
}

type responseFrame struct {
	Version int            `json:"version"`
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Status  DeliveryStatus `json:"status"`
	Error   string         `json:"error,omitempty"`
}

type SendResult struct {
	MessageID string
	Peer      Peer
	Status    DeliveryStatus
}

type StatusEvent struct {
	MessageID string
	Peer      Peer
	Status    DeliveryStatus
}
