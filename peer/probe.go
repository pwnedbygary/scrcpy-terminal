package peer

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Probe inspects a paired target without opening media or acquiring control.
// A single five-second deadline covers DNS, TCP, TLS and the reply.
func (c *Client) Probe(ctx context.Context, host string, port int, pin Fingerprint) (*Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, _, err := c.dialContext(ctx, host, port, &pin)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := WriteFrame(conn, &Message{Type: TypeProbe, V: ProtocolVersion, MinV: ProtocolVersion}); err != nil {
		return nil, err
	}
	reply, err := ReadFrame(conn)
	if err != nil {
		return nil, err
	}
	if reply.Type == TypeReject {
		return nil, &RejectError{Code: reply.Code, Message: reply.Message}
	}
	if reply.Type != TypeProbeResult || reply.V != ProtocolVersion || reply.Ready == nil || reply.Device == nil {
		return nil, fmt.Errorf("malformed probe result")
	}
	return reply, nil
}

// ProbeLabel is shared by callers so readiness never means full control.
func ProbeLabel(result *Message, err error) string {
	if err != nil {
		if errors.Is(err, ErrIdentityMismatch) {
			return "Identity mismatch"
		}
		if r, ok := err.(*RejectError); ok {
			switch r.Code {
			case "bad_request", "unsupported", "version":
				return "Status unavailable—update the peer"
			case "not_paired":
				return "Pairing no longer accepted"
			case "forbidden":
				return "Viewing permission missing"
			}
		}
		return "Not reachable: " + err.Error()
	}
	if !hasGrant(result.Grants, GrantView) {
		return "Viewing permission missing"
	}
	if !*result.Ready {
		if result.Reason != "" {
			return result.Reason
		}
		return "Serving not ready"
	}
	if result.Caps == nil || !result.Caps.Video {
		return "Video unavailable"
	}
	if result.ControlOccupied {
		return "Ready—another device has control"
	}
	return "Ready"
}
