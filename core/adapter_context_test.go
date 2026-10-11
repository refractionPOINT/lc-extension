package core

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
)

const (
	adapterTestSecret = "test-secret"
	adapterTestName   = "test-extension"
)

func TestWebhookContextArrayProtocol(t *testing.T) {
	got := make(chan []map[string]string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("lc-secret") != adapterTestSecret || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect webhook protocol")
		}
		z, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer func() { _ = z.Close() }()
		var events []map[string]string
		if err := json.NewDecoder(z).Decode(&events); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		got <- events
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	data := []map[string]string{{"event_type": "created", "id": "1"}, {"event_type": "closed", "id": "2"}}
	if err := sendWebhookWithContext(context.Background(), srv.Client(), srv.URL, adapterTestSecret, data); err != nil {
		t.Fatal(err)
	}
	if events := <-got; len(events) != 2 || events[0]["id"] != "1" || events[1]["id"] != "2" {
		t.Fatalf("array changed: %v", events)
	}
}

func TestWebhookContextCancelsInflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- sendWebhookWithContext(ctx, srv.Client(), srv.URL, adapterTestSecret, map[string]string{"id": "1"})
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook ignored cancellation")
	}
}

func TestWebhookContextFailureAndURLCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if err := sendWebhookWithContext(context.Background(), srv.Client(), srv.URL, adapterTestSecret, []string{adapterTestName}); err == nil {
		t.Fatal("503 accepted")
	}
	ms := limacharlie.NewMockServer("test-org")
	defer ms.Close()
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatal(err)
	}
	defer org.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ext := &Extension{ExtensionName: adapterTestName, SecretKey: adapterTestName}
	if err := ext.SendToWebhookAdapterWithContext(ctx, org, map[string]string{"id": "1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("URL lookup ignored cancellation: %v", err)
	}
}

func TestWebhookHostSendWithoutOrg(t *testing.T) {
	const oid = "test-oid"
	ext := &Extension{ExtensionName: "test extension", SecretKey: adapterTestSecret}
	got := make(chan *http.Request, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	saved := webhookClient
	webhookClient = srv.Client()
	defer func() { webhookClient = saved }()
	host := strings.TrimPrefix(srv.URL, "https://")
	if err := ext.SendToWebhookHostWithContext(context.Background(), oid, host, map[string]string{"id": "1"}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.URL.EscapedPath() != "/test-oid/test%20extension" {
		t.Fatalf("path=%q", r.URL.EscapedPath())
	}
	if r.Header.Get("lc-secret") != ext.generateWebhookSecretForOrg(oid) {
		t.Fatal("secret is not the org's derived webhook secret")
	}
	if err := ext.SendToWebhookHostWithContext(context.Background(), oid, "", nil); err == nil {
		t.Fatal("empty hooks host accepted")
	}
}
