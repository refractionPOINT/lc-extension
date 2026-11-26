package multiplexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A worker answer that does not decode can echo the request it was sent,
// Secret parameters included: the error built from it must not carry it.
func TestForwardHTTPDecodeErrorCarriesNoBody(t *testing.T) {
	const fakeSecret = "FAKE-SECRET-PARAM-VALUE-0001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>upstream echoed " + fakeSecret + "</html>"))
	}))
	defer server.Close()

	_, err := forwardHTTP(context.Background(), []byte("s"), server.Client(), server.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if strings.Contains(err.Error(), fakeSecret) {
		t.Fatalf("the error carries the body: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected the status in the error: %v", err)
	}
}
