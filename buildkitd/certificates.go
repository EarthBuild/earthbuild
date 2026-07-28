package buildkitd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/EarthBuild/earthbuild/util/hint"
)

type certData struct {
	Key  *rsa.PrivateKey
	Cert *x509.Certificate
}

const (
	buildkit = "buildkit"
	earthly  = "earthly"

	typeCert   = "CERTIFICATE"
	typeRSAKey = "RSA PRIVATE KEY"
)

// GenCerts creates and saves a CA and certificates for both sides of an mTLS TCP connection.
func GenCerts(cfg config.Config, hostname string) error {
	caKey, err := parseTLSKey(cfg.Global.TLSCAKey)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed reading CA key: %w", err)
	}

	if errors.Is(err, os.ErrNotExist) {
		all := []string{
			cfg.Global.TLSCACert,
			cfg.Global.ServerTLSCert,
			cfg.Global.ServerTLSKey,
			cfg.Global.ClientTLSCert,
			cfg.Global.ClientTLSKey,
		}

		var missing []string

		for _, f := range all {
			if exists, _ := fileutil.FileExists(f); !exists {
				missing = append(missing, f)
			}
		}

		switch len(missing) {
		case 0:
			return nil
		case len(all):
			caKey, err = createTLSKey(cfg.Global.TLSCAKey)
			if err != nil {
				return fmt.Errorf("could not create CA: %w", err)
			}
		default:
			found := all
			for _, m := range missing {
				for i, f := range found {
					if f == m {
						found = append(found[:i], found[i+1:]...)
						break
					}
				}
			}

			return hint.Wrap(
				errors.New("cannot generate missing certificates"),
				fmt.Sprintf("missing certificates: %v", missing),
				fmt.Sprintf("found certificates: %v", found),
				"you may want to stop earthly-buildkitd, delete your certificates, "+
					"and run 'earthly bootstrap' to regenerate certificates",
			)
		}
	}

	caCert, err := parseTLSCert(cfg.Global.TLSCACert)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not parse CA certificate: %w", err)
	}

	if errors.Is(err, os.ErrNotExist) {
		caCert, err = createCACert(caKey, cfg.Global.TLSCACert)
		if err != nil {
			return fmt.Errorf("could not create CA certificate: %w", err)
		}
	}

	ca := &certData{
		Key:  caKey,
		Cert: caCert,
	}

	err = genCert(ca, buildkit, hostname, cfg.Global.ServerTLSKey, cfg.Global.ServerTLSCert)
	if err != nil {
		return fmt.Errorf("could not generate server TLS key/cert pair for %v: %w", buildkit, err)
	}

	err = genCert(ca, earthly, hostname, cfg.Global.ClientTLSKey, cfg.Global.ClientTLSCert)
	if err != nil {
		return fmt.Errorf("could not generate client TLS key/cert pair for %v: %w", earthly, err)
	}

	return nil
}

func genCert(ca *certData, role, hostname, keyPath, certPath string) error {
	certExists, _ := fileutil.FileExists(certPath)

	key, err := parseTLSKey(keyPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not parse %v TLS key: %w", role, err)
	}

	if errors.Is(err, os.ErrNotExist) {
		if certExists {
			return fmt.Errorf("refusing to generate TLS key %q: TLS cert %q exists: %w", keyPath, certPath, err)
		}

		key, err = createTLSKey(keyPath)
		if err != nil {
			return fmt.Errorf("could not create %v TLS key: %w", role, err)
		}
	}

	if !certExists {
		_, err = createTLSCert(ca, key, role, certPath, hostname)
		if err != nil {
			return fmt.Errorf("could not create %v TLS cert: %w", role, err)
		}
	}

	return nil
}

func parseTLSKey(path string) (*rsa.PrivateKey, error) {
	body, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("could not read private key %q: %w", path, err)
	}

	dec, _ := pem.Decode(body)

	key, err := x509.ParsePKCS1PrivateKey(dec.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not decode %q as RSA private key: %w", path, err)
	}

	return key, nil
}

func createTLSKey(path string) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("could not generate RSA key: %w", err)
	}

	err = savePEM(path, typeRSAKey, x509.MarshalPKCS1PrivateKey(key))
	if err != nil {
		return nil, fmt.Errorf("saving private key to %q failed: %w", path, err)
	}

	return key, nil
}

func parseTLSCert(path string) (*x509.Certificate, error) {
	body, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("could not read public cert %q: %w", path, err)
	}

	dec, _ := pem.Decode(body)

	cert, err := x509.ParseCertificate(dec.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not decode %q as x509 certificate: %w", path, err)
	}

	return cert, nil
}

func createTLSCert(ca *certData, key *rsa.PrivateKey, role, path, hostname string) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, fmt.Errorf("could not generate serial for role %q: %w", role, err)
	}

	cert := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{fmt.Sprintf("EarthBuild GRPC: %v side", role)},
		},
		DNSNames:     []string{hostname},
		IPAddresses:  []net.IP{net.IPv6loopback, net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte(role),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("could not generate certificate for role %q: %w", role, err)
	}

	err = savePEM(path, typeCert, certBytes)
	if err != nil {
		return nil, fmt.Errorf("could not save certificate for role %q to path %q: %w", role, path, err)
	}

	return cert, nil
}

func createCACert(key *rsa.PrivateKey, path string) (*x509.Certificate, error) {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2021),
		Subject: pkix.Name{
			Organization: []string{"earth Buildkit GRPC CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("creating CA certificate failed: %w", err)
	}

	err = savePEM(path, typeCert, caBytes)
	if err != nil {
		return nil, fmt.Errorf("saving CA certificate to %q failed: %w", path, err)
	}

	return ca, nil
}

func savePEM(path, typ string, bytes []byte) error {
	err := os.MkdirAll(filepath.Dir(path), 0o755) // #nosec G301
	if err != nil {
		return err
	}

	f, err := os.Create(path) // #nosec G304
	if err != nil {
		return err
	}
	defer f.Close()

	err = pem.Encode(f, &pem.Block{
		Type:  typ,
		Bytes: bytes,
	})
	if err != nil {
		return err
	}

	err = f.Chmod(0o444)
	if err != nil {
		return err
	}

	return nil
}
