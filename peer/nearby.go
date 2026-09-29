package peer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Nearby pairing: instead of typing an invitation, both users compare a
// six-digit code derived from the two TLS identities and a nonce from each
// side. The target commits to its nonce before it sees ours, and we send ours
// before it reveals, so a relay terminating TLS on both legs cannot make the
// two codes agree: one chance in a million per attempt, and every attempt
// needs the target's user to accept it. The derivation matches the Android
// app's ShortCode; see "Nearby pairing" in docs/PEER_PROTOCOL.md.

// NonceSize is the length of each side's nonce.
const NonceSize = 32

const (
	shortCodeLabel = "scterm-code-v1"
	// answerTimeout outlasts the target's own wait for its user (60 s), so
	// its refusal arrives before this side gives up.
	answerTimeout = 90 * time.Second
)

// ErrPairingCancelled is the result of a pairing this side abandoned.
var ErrPairingCancelled = errors.New("pairing cancelled")

// Commitment is what the target sends before seeing the controller's nonce.
func Commitment(targetNonce []byte, target, controller Fingerprint) []byte {
	h := sha256.New()
	h.Write([]byte(shortCodeLabel + "\x00commit\x00"))
	h.Write(targetNonce)
	h.Write(target[:])
	h.Write(controller[:])
	return h.Sum(nil)
}

// ShortCode is the six digits both screens show.
func ShortCode(controller, target Fingerprint, controllerNonce, targetNonce []byte) string {
	h := sha256.New()
	h.Write([]byte(shortCodeLabel + "\x00code\x00"))
	h.Write(controller[:])
	h.Write(target[:])
	h.Write(controllerNonce)
	h.Write(targetNonce)
	sum := h.Sum(nil)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1_000_000)
}

// DisplayCode groups a code as both screens show it: "482915" -> "482 915".
func DisplayCode(code string) string {
	if len(code) != 6 {
		return code
	}
	return code[:3] + " " + code[3:]
}

// NearbyPairing is a pairing waiting on both users, whose screens show Code.
type NearbyPairing struct {
	Code   string
	Target Fingerprint
	Device DeviceInfo

	conn      *tls.Conn
	confirmed atomic.Bool
	cancelled atomic.Bool
	result    chan NearbyResult
}

// NearbyResult is how a nearby pairing ended.
type NearbyResult struct {
	Pair PairResult
	Err  error
}

// StartNearbyPairing asks a target that has an invitation open to pair by
// comparing codes, and returns once both sides can show the code.
func (c *Client) StartNearbyPairing(host string, port int, servePort int) (*NearbyPairing, error) {
	conn, target, err := c.dial(host, port, nil)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*NearbyPairing, error) {
		conn.Close()
		return nil, err
	}
	if err := WriteFrame(conn, &Message{Type: TypePairNearby, V: ProtocolVersion, Client: &c.Info, ServePort: servePort}); err != nil {
		return fail(err)
	}
	commit, err := ReadFrame(conn)
	if err != nil {
		return fail(err)
	}
	switch commit.Type {
	case TypeCodeCommit:
	case TypeReject:
		return fail(&RejectError{Code: commit.Code, Message: commit.Message})
	default:
		return fail(fmt.Errorf("unexpected reply %q to pair_nearby", commit.Type))
	}
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fail(err)
	}
	if err := WriteFrame(conn, &Message{Type: TypeCodeNonce, Nonce: hex.EncodeToString(nonce)}); err != nil {
		return fail(err)
	}
	reveal, err := ReadFrame(conn)
	if err != nil {
		return fail(err)
	}
	if reveal.Type != TypeCodeReveal {
		return fail(fmt.Errorf("unexpected reply %q instead of code_reveal", reveal.Type))
	}
	targetNonce, err := hex.DecodeString(reveal.Nonce)
	if err != nil || len(targetNonce) != NonceSize {
		return fail(errors.New("malformed code_reveal"))
	}
	want := hex.EncodeToString(Commitment(targetNonce, target, c.Identity.Fingerprint))
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(commit.Commit))) != 1 {
		return fail(&RejectError{Code: "bad_proof", Message: "the device broke the pairing protocol"})
	}
	p := &NearbyPairing{
		Code:   ShortCode(c.Identity.Fingerprint, target, nonce, targetNonce),
		Target: target,
		conn:   conn,
		result: make(chan NearbyResult, 1),
	}
	if reveal.Device != nil {
		p.Device = *reveal.Device
	}
	conn.SetDeadline(time.Now().Add(answerTimeout))
	go p.await()
	return p, nil
}

// await reads the target's answer, which may come before Confirm when the
// target's user declines first.
func (p *NearbyPairing) await() {
	defer p.conn.Close()
	reply, err := ReadFrame(p.conn)
	var r NearbyResult
	switch {
	case err != nil && p.cancelled.Load():
		r.Err = ErrPairingCancelled
	case err != nil:
		r.Err = err
	case reply.Type == TypePaired && !p.confirmed.Load():
		r.Err = errors.New("the device did not wait for this computer's confirmation")
	case reply.Type == TypePaired:
		r.Pair = PairResult{Fingerprint: p.Target, Device: p.Device, Grants: reply.Grants}
		if reply.Device != nil {
			r.Pair.Device = *reply.Device
		}
	case reply.Type == TypeReject:
		r.Err = &RejectError{Code: reply.Code, Message: reply.Message}
	default:
		r.Err = fmt.Errorf("unexpected pairing reply %q", reply.Type)
	}
	p.result <- r
}

// Result delivers how the pairing ended, once.
func (p *NearbyPairing) Result() <-chan NearbyResult { return p.result }

// Confirm tells the target that this side's user saw the same code.
func (p *NearbyPairing) Confirm() error {
	p.confirmed.Store(true)
	return WriteFrame(p.conn, &Message{Type: TypeCodeConfirm})
}

// Cancel abandons the pairing; the target stops asking its user.
func (p *NearbyPairing) Cancel() {
	p.cancelled.Store(true)
	p.conn.Close()
}
