package peer

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// ProtocolVersion is the only version this client speaks.
const ProtocolVersion = 1

const (
	maxFrame = 64 << 10
	// envelopeType marks a peer event on the control channel; scrcpy never assigns it.
	envelopeType = 0xFE
)

// Message types, as in the JSON "type" field.
const (
	TypeHello    = "hello"
	TypeWelcome  = "welcome"
	TypeReject   = "reject"
	TypePair     = "pair"
	TypePaired   = "paired"
	TypePing     = "ping"
	TypePong     = "pong"
	TypeTakeover = "takeover"
	TypeLease    = "lease"
	TypeError    = "error"
	TypeStatus   = "status"
	TypeBye      = "bye"
)

// Channels, one TCP connection each.
const (
	ChannelControl = "control"
	ChannelVideo   = "video"
	ChannelAudio   = "audio"
)

// Lease states: HELD means this controller's input goes through.
const (
	LeaseHeld   = "held"
	LeaseViewer = "viewer"
)

// Grants a target gives one controller.
const (
	GrantView      = "view"
	GrantAudio     = "audio"
	GrantControl   = "control"
	GrantClipboard = "clipboard"
)

type ClientInfo struct {
	Name     string `json:"name"`
	App      string `json:"app"`
	Platform string `json:"platform,omitempty"`
}

type DeviceInfo struct {
	Name         string `json:"name"`
	Model        string `json:"model,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	SDK          int    `json:"sdk,omitempty"`
}

// StreamRequest is what a controller asks for; the target clamps it to its
// grants and capabilities.
type StreamRequest struct {
	Video        bool     `json:"video"`
	Audio        bool     `json:"audio"`
	Control      bool     `json:"control"`
	MaxSize      int      `json:"maxSize,omitempty"`
	MaxFPS       float64  `json:"maxFps,omitempty"`
	VideoBitRate int      `json:"videoBitRate,omitempty"`
	VideoCodecs  []string `json:"videoCodecs,omitempty"`
	AudioCodecs  []string `json:"audioCodecs,omitempty"`
}

type Capabilities struct {
	Backend     string   `json:"backend"`
	Video       bool     `json:"video"`
	Audio       bool     `json:"audio"`
	Control     []string `json:"control"`
	Multitouch  bool     `json:"multitouch"`
	Clipboard   bool     `json:"clipboard"`
	MaxSessions int      `json:"maxSessions"`
	Notes       []string `json:"notes"`
}

type StreamsState struct {
	Video      bool   `json:"video"`
	Audio      bool   `json:"audio"`
	Control    bool   `json:"control"`
	AudioError string `json:"audioError,omitempty"`
}

// Message is every handshake and envelope message: Type decides which fields
// mean anything, exactly as in the protocol's JSON. Unknown fields are ignored.
type Message struct {
	Type        string         `json:"type"`
	V           int            `json:"v,omitempty"`
	MinV        int            `json:"minV,omitempty"`
	Channel     string         `json:"channel,omitempty"`
	Session     string         `json:"session,omitempty"`
	Token       string         `json:"token,omitempty"`
	Request     *StreamRequest `json:"request,omitempty"`
	Client      *ClientInfo    `json:"client,omitempty"`
	Grants      []string       `json:"grants,omitempty"`
	Caps        *Capabilities  `json:"caps,omitempty"`
	Device      *DeviceInfo    `json:"device,omitempty"`
	Streams     *StreamsState  `json:"streams,omitempty"`
	Lease       string         `json:"lease,omitempty"`
	Proof       string         `json:"proof,omitempty"`
	ServePort   int            `json:"servePort,omitempty"`
	Code        string         `json:"code,omitempty"`
	Message     string         `json:"message,omitempty"`
	ControlType string         `json:"controlType,omitempty"`
	T           int64          `json:"t,omitempty"`
	State       string         `json:"state,omitempty"`
	Holder      string         `json:"holder,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	EndSession  bool           `json:"endSession,omitempty"`
}

// RejectError is a target's explicit refusal; Code is one of the protocol's
// reject codes (not_paired, bad_proof, busy, ...).
type RejectError struct {
	Code    string
	Message string
}

func (e *RejectError) Error() string {
	if e.Message == "" {
		return "rejected: " + e.Code
	}
	return fmt.Sprintf("rejected (%s): %s", e.Code, e.Message)
}

func encodeFrame(m *Message) ([]byte, error) {
	js, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(js) > maxFrame {
		return nil, fmt.Errorf("peer frame of %d bytes", len(js))
	}
	out := make([]byte, 4+len(js))
	binary.BigEndian.PutUint32(out, uint32(len(js)))
	copy(out[4:], js)
	return out, nil
}

// WriteFrame writes one handshake frame: u32 big-endian length, then JSON.
func WriteFrame(w io.Writer, m *Message) error {
	b, err := encodeFrame(m)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadFrame reads one handshake frame, bounded to 64 KiB.
func ReadFrame(r io.Reader) (*Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	return readFrameBody(r, binary.BigEndian.Uint32(hdr[:]))
}

func readFrameBody(r io.Reader, n uint32) (*Message, error) {
	if n > maxFrame {
		return nil, fmt.Errorf("peer frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("truncated peer frame: %w", err)
	}
	var m Message
	if err := json.Unmarshal(buf, &m); err != nil || m.Type == "" {
		return nil, fmt.Errorf("malformed peer message")
	}
	return &m, nil
}

// encodeEnvelope is a peer event on the control channel: 0xFE, then a frame.
func encodeEnvelope(m *Message) ([]byte, error) {
	frame, err := encodeFrame(m)
	if err != nil {
		return nil, err
	}
	return append([]byte{envelopeType}, frame...), nil
}
