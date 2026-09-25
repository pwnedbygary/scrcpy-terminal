// Package peer is the Go side of the scterm peer protocol
// (docs/PEER_PROTOCOL.md): pairing with, and connecting to, an scterm target
// such as the Android app, over mutual TLS with pinned identities. Media and
// control keep the scrcpy v4.1 wire format, so a Session hands the rest of
// scterm connections that read exactly like the adb tunnel's sockets.
package peer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Fingerprint identifies a peer: SHA-256 of its certificate's
// SubjectPublicKeyInfo (DER). Names and dates in certificates mean nothing.
type Fingerprint [32]byte

// FingerprintOf hashes a SubjectPublicKeyInfo.
func FingerprintOf(spki []byte) Fingerprint { return sha256.Sum256(spki) }

// ParseFingerprint accepts the 64-digit hex form, with or without ':' separators.
func ParseFingerprint(s string) (Fingerprint, error) {
	var fp Fingerprint
	b, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, ":", ""))))
	if err != nil || len(b) != len(fp) {
		return fp, fmt.Errorf("fingerprint must be %d hex digits", 2*len(fp))
	}
	copy(fp[:], b)
	return fp, nil
}

// Hex is the canonical form, used on the wire and in storage.
func (f Fingerprint) Hex() string { return hex.EncodeToString(f[:]) }

// Short is the 80-bit prefix people compare between two screens.
func (f Fingerprint) Short() string { return group4(Base32Encode(f[:10])) }

// Equal compares in constant time.
func (f Fingerprint) Equal(o Fingerprint) bool { return subtle.ConstantTimeCompare(f[:], o[:]) == 1 }

// Identity is this installation's long-term key and self-signed certificate.
type Identity struct {
	Certificate tls.Certificate
	Fingerprint Fingerprint
}

// LoadOrCreateIdentity keeps the key and certificate in dir/identity.pem
// (mode 0600), creating an EC P-256 pair on first use.
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, "identity.pem")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if data, err = newIdentityPEM(); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = writeFileAtomic(path, data, 0o600)
		}
	}
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(data, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Identity{Certificate: cert, Fingerprint: FingerprintOf(leaf.RawSubjectPublicKeyInfo)}, nil
}

func newIdentityPEM() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "scterm"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(100, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...), nil
}

// writeFileAtomic replaces path in one rename, so a crash never leaves half a file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Base32Encode is Crockford base32 without padding.
func Base32Encode(data []byte) string {
	var sb strings.Builder
	buffer, bits := 0, 0
	for _, b := range data {
		buffer = buffer<<8 | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(crockford[(buffer>>bits)&31])
		}
		buffer &= 1<<bits - 1
	}
	if bits > 0 {
		sb.WriteByte(crockford[(buffer<<(5-bits))&31])
	}
	return sb.String()
}

// Base32Decode ignores '-' and white space, is case-insensitive and reads
// O as 0 and I/L as 1, so a code can be typed the way it is shown.
func Base32Decode(text string) ([]byte, error) {
	out := make([]byte, 0, len(text)*5/8+1)
	buffer, bits := 0, 0
	for _, c := range text {
		if c == '-' || unicode.IsSpace(c) {
			continue
		}
		v := base32Value(c)
		if v < 0 {
			return nil, fmt.Errorf("invalid code character %q", c)
		}
		buffer = buffer<<5 | v
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buffer>>bits))
		}
		buffer &= 1<<bits - 1
	}
	return out, nil
}

func base32Value(c rune) int {
	switch u := unicode.ToUpper(c); u {
	case 'O':
		return 0
	case 'I', 'L':
		return 1
	default:
		return strings.IndexRune(crockford, u)
	}
}

func group4(s string) string {
	var sb strings.Builder
	for i, c := range s {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteRune(c)
	}
	return sb.String()
}
