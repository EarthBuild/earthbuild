package guest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// actionsTLS is a certificate for this step's service, and where its client
// should look for it.
//
// **Because the client insists, not because there is anything to protect.** The
// listener is reachable only from inside a sandbox this engine started, which
// is the whole authorisation model - but Buck2's remote-execution client speaks
// TLS unconditionally and has no plaintext setting, so a service without a
// certificate is a service it reports as a corrupt message and cannot use.
//
// Made per step and never written anywhere durable: the certificate goes beside
// the socket, on the ephemeral tmpfs a WITH RE step is given, so it dies with
// the step and cannot reach a layer. A key that outlived the thing it
// authenticates would be a credential this engine had left lying in a build.
//
// Self-signed, and the client is told to trust exactly this one. A CA bundle
// would be a second thing to manage for a connection that never leaves the
// machine.
func actionsTLS(at string) (*tls.Config, error) {
	// **A CA and a leaf it signed, not one certificate doing both.** A client
	// handed a single self-signed certificate as its trust anchor refuses it
	// the moment the server presents the same one as its own: rustls calls that
	// `CaUsedAsEndEntity` and buck2 reports it as a connection error. The same
	// two-certificate shape `buildkitd/certificates.go` builds, for the same
	// reason, in about a tenth of the code because none of it is durable.
	ca, caKey, err := selfSigned("earthbuild actions CA", nil)
	if err != nil {
		return nil, err
	}

	leaf, leafKey, err := selfSigned("earthbuild actions", &signer{ca, caKey})
	if err != nil {
		return nil, err
	}

	// Where the client is told to look: the CA only. Beside the socket, because
	// that is the directory this step was given and the one that disappears
	// with it.
	if err := os.WriteFile(at, pemOf("CERTIFICATE", ca.Raw), 0o644); err != nil { //nolint:gosec // the step must read it
		return nil, fmt.Errorf("write the action service's certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, fmt.Errorf("encode the action service's key: %w", err)
	}

	pair, err := tls.X509KeyPair(
		append(pemOf("CERTIFICATE", leaf.Raw), pemOf("CERTIFICATE", ca.Raw)...),
		pemOf("EC PRIVATE KEY", keyDER))
	if err != nil {
		return nil, fmt.Errorf("load the action service's certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
		// gRPC is HTTP/2 and negotiates it by ALPN; without this a client
		// completes the handshake and then finds no protocol it can speak.
		NextProtos: []string{"h2"},
	}, nil
}

// ActionsCertPath is where a step finds the certificate of its own service.
func ActionsCertPath(socket string) string {
	return filepath.Join(filepath.Dir(socket), "ca.pem")
}

// signer is the certificate and key a leaf is signed by.
type signer struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// selfSigned makes a certificate, signed by `by` or by itself where that is nil.
//
// Self-signed means a CA; signed by something means a leaf. That is the only
// difference between the two this needs, so it is the only thing that varies.
func selfSigned(name string, by *signer) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("make a key for %s: %w", name, err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("make a serial for %s: %w", name, err)
	}

	// A day, which is longer than any step and shorter than anything that could
	// be mistaken for a durable credential.
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
	}

	parent, signWith := tmpl, key

	switch by {
	case nil:
		tmpl.IsCA = true
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	default:
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
		parent, signWith = by.cert, by.key
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signWith)
	if err != nil {
		return nil, nil, fmt.Errorf("make a certificate for %s: %w", name, err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("read back the certificate for %s: %w", name, err)
	}

	return cert, key, nil
}

func pemOf(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}
