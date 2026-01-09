package main

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func TestService(t *testing.T) {
	t.Parallel()

	go main()
	time.Sleep(time.Second) // Leave time for service to start

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
