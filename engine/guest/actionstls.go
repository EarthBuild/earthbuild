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
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("make a key for the action service: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("make a serial for the action service: %w", err)
	}

	// A day, which is longer than any step and shorter than anything that could
	// be mistaken for a durable credential.
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "earthbuild actions"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("make a certificate for the action service: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	// Where the client is told to look. Beside the socket, because that is the
	// directory this step was given and the one that disappears with it.
	if err := os.WriteFile(at, certPEM, 0o644); err != nil { //nolint:gosec // the step must read it
		return nil, fmt.Errorf("write the action service's certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode the action service's key: %w", err)
	}

	pair, err := tls.X509KeyPair(certPEM,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
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
