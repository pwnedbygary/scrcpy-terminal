package peer

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// DefaultPort is outside 27183..27282, which scterm binds for its adb tunnels.
const DefaultPort = 27300

const secretBytes = 10

// Invitation is how to reach a target waiting to pair, plus its one-time
// 80-bit secret. Fingerprint, when present, pins the target's identity.
type Invitation struct {
	Host        string
	Port        int
	Secret      []byte
	Fingerprint *Fingerprint
}

// ParseInvitation reads the typed form ("host:port CODE") or an
// scterm://pair link.
func ParseInvitation(text string) (Invitation, error) {
	t := strings.TrimSpace(text)
	if len(t) >= 9 && strings.EqualFold(t[:9], "scterm://") {
		return parseInvitationURI(t)
	}
	fields := strings.Fields(t)
	if len(fields) < 2 {
		return Invitation{}, errors.New(`expected "host:port CODE"`)
	}
	host, port, err := ParseAddress(fields[0], DefaultPort)
	if err != nil {
		return Invitation{}, err
	}
	secret, err := parseCode(strings.Join(fields[1:], ""))
	if err != nil {
		return Invitation{}, err
	}
	return Invitation{Host: host, Port: port, Secret: secret}, nil
}

func parseInvitationURI(uri string) (Invitation, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return Invitation{}, err
	}
	q := u.Query()
	inv := Invitation{Host: q.Get("h"), Port: DefaultPort}
	if inv.Host == "" {
		return Invitation{}, errors.New("invitation has no host")
	}
	if p := q.Get("p"); p != "" {
		if inv.Port, err = parsePort(p); err != nil {
			return Invitation{}, err
		}
	}
	if q.Get("c") == "" {
		return Invitation{}, errors.New("invitation has no code")
	}
	if inv.Secret, err = parseCode(q.Get("c")); err != nil {
		return Invitation{}, err
	}
	if f := q.Get("f"); f != "" {
		fp, err := ParseFingerprint(f)
		if err != nil {
			return Invitation{}, err
		}
		inv.Fingerprint = &fp
	}
	return inv, nil
}

func parseCode(code string) ([]byte, error) {
	secret, err := Base32Decode(code)
	if err != nil {
		return nil, err
	}
	if len(secret) != secretBytes {
		return nil, errors.New("pairing code must be 16 characters")
	}
	return secret, nil
}

// ParseAddress reads host, host:port, [v6] or [v6]:port; a bare IPv6
// literal gets defaultPort.
func ParseAddress(text string, defaultPort int) (string, int, error) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "[") {
		end := strings.IndexByte(t, ']')
		if end <= 1 {
			return "", 0, fmt.Errorf("invalid address %s", t)
		}
		rest := strings.TrimPrefix(t[end+1:], ":")
		if rest == "" {
			return t[1:end], defaultPort, nil
		}
		port, err := parsePort(rest)
		return t[1:end], port, err
	}
	switch strings.Count(t, ":") {
	case 0:
		if t == "" {
			return "", 0, errors.New("missing host")
		}
		return t, defaultPort, nil
	case 1:
		host, p, _ := strings.Cut(t, ":")
		port, err := parsePort(p)
		return host, port, err
	default:
		return t, defaultPort, nil
	}
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %s", s)
	}
	return p, nil
}

// Code is the secret as shown for typing: XXXX-XXXX-XXXX-XXXX.
func (i Invitation) Code() string { return group4(Base32Encode(i.Secret)) }

// Address is host:port, bracketing IPv6 literals.
func (i Invitation) Address() string { return net.JoinHostPort(i.Host, strconv.Itoa(i.Port)) }

// ManualText is what a person types on the other device.
func (i Invitation) ManualText() string { return i.Address() + " " + i.Code() }

// URI is the scterm://pair link form.
func (i Invitation) URI() string {
	s := "scterm://pair?h=" + url.QueryEscape(i.Host) + "&p=" + strconv.Itoa(i.Port) + "&c=" + Base32Encode(i.Secret)
	if i.Fingerprint != nil {
		s += "&f=" + i.Fingerprint.Hex()
	}
	return s
}

// Pairing roles, as bound into each side's proof.
const (
	RoleController = "controller"
	RoleTarget     = "target"
)

// PairingProof is HMAC-SHA256(secret, "scterm-pair-v1\0" role "\0" self peer)
// over the two fingerprints seen on this TLS connection: a relay terminating
// TLS shows each side a different certificate, so it cannot reuse a proof.
func PairingProof(secret []byte, role string, self, peer Fingerprint) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("scterm-pair-v1\x00" + role + "\x00"))
	mac.Write(self[:])
	mac.Write(peer[:])
	return mac.Sum(nil)
}
