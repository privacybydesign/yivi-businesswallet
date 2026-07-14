package qerds

// Direction of a message relative to the owning organization.
const (
	DirectionOutbound = "outbound"
	DirectionInbound  = "inbound"
)

// Outbound delivery lifecycle: draft -> submitted -> accepted -> delivered ->
// read, with terminal failed / expired. Each transition is anchored to a piece
// of evidence carrying its qualified timestamp.
const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusAccepted  = "accepted"
	StatusDelivered = "delivered"
	StatusRead      = "read"
	StatusFailed    = "failed"
	StatusExpired   = "expired"
)

// Inbound lifecycle: received -> read.
const (
	StatusReceived = "received"
)
