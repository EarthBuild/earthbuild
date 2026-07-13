package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	go main()

	// Wait until the http server is ready
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := net.Dialer{}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", "localhost:8080")
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			log.Fatal("timed out waiting for service to start")
		case <-time.After(10 * time.Millisecond):
		}
	}

	os.Exit(m.Run())
}

func TestService(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost:8080/two/hello", nil)
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	defer resp.Body.Close()

	expected := "Hello, Friend!"

	actual, _ := io.ReadAll(resp.Body)
	if expected != string(actual) {
		t.Fail()
	}
}
