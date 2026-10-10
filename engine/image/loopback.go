package image

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
)

// loopbackRegistry reports a registry on this machine: `localhost`, or an
// address in 127.0.0.0/8 or ::1, with or without a port.
//
// By name for `localhost` alone. `localhost.example.com` is somebody else's
// machine, and resolving names here would make the answer depend on this
// machine's resolver.
func loopbackRegistry(registry string) bool {
	host := registry
	if h, _, err := net.SplitHostPort(registry); err == nil {
		host = h
	}

	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// schemeOf is the scheme to speak to a registry in.
//
// **HTTPS, except to a registry on this machine that answers in HTTP.** Docker's
// own local registry, `registry:2`, serves plain HTTP unless it is given a
// certificate, and containerd's rule for it is the one here: try HTTPS, and
// fall back only for a loopback host. Plain HTTP anywhere else is a pull an
// intermediary can rewrite.
//
// The fallback is taken on exactly one answer - the server replied in HTTP to a
// TLS handshake. A refused connection, a status, and above all a certificate
// this machine does not trust are all HTTPS's answer, never a reason to try
// again without it.
//
// One probe per call rather than a table kept between them: loopback is
// sub-millisecond, and a remembered answer about a port outlives whatever was
// listening on it.
func schemeOf(ctx context.Context, client *http.Client, registry string, plain bool) string {
	if plain {
		return schemePlain
	}

	if !loopbackRegistry(registry) {
		return schemeHTTPS
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, schemeHTTPS+"://"+registry+"/v2/", nil)
	if err != nil {
		return schemeHTTPS
	}

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()

		return schemeHTTPS
	}

	if answeredInHTTP(err) {
		return schemePlain
	}

	return schemeHTTPS
}

// answeredInHTTP reports a TLS handshake the server answered in plain HTTP.
//
// Go's transport turns that case into an unexported error whose text is the
// only handle on it; the record-header error is what it wraps when it does not.
func answeredInHTTP(err error) bool {
	if strings.Contains(err.Error(), "server gave HTTP response to HTTPS client") {
		return true
	}

	var rh tls.RecordHeaderError

	return errors.As(err, &rh) && strings.HasPrefix(string(rh.RecordHeader[:]), "HTTP/")
}
