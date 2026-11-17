package simplified

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/core"
)

// fakeHookGateway accepts a hook only when its lc-secret matches the secret
// stored in the org's cloud_sensor record, answering like the real webhook
// gateway otherwise.
type fakeHookGateway struct {
	t   *testing.T
	org *limacharlie.Organization

	// refreshing503 makes the gateway answer this many requests, after the
	// first rejection, with the 503 it returns while applying an adapter change.
	refreshing503 int

	mu          sync.Mutex
	accepted    int
	rejected    int
	unavailable int
}

func (g *fakeHookGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		g.t.Errorf("unexpected hook path %q", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rec, err := limacharlie.NewHiveClient(g.org).Get(limacharlie.HiveArgs{
		HiveName:     "cloud_sensor",
		PartitionKey: parts[0],
		Key:          parts[1],
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rejected > 0 && g.refreshing503 > 0 {
		g.refreshing503--
		g.unavailable++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"webhook configuration unavailable; retry later"}`))
		return
	}
	if err != nil {
		g.rejected++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized, unknown hook"}`))
		return
	}
	webhook, _ := rec.Data["webhook"].(map[string]interface{})
	if webhook == nil || webhook["secret"] != r.Header.Get("lc-secret") {
		g.rejected++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized, wrong secret"}`))
		return
	}
	g.accepted++
	w.WriteHeader(http.StatusOK)
}

// The content of the hook does not matter to the adapter re-sync.
var testHook = limacharlie.Dict{"k": "v"}

func newRotationFixture(t *testing.T) (*limacharlie.MockServer, *limacharlie.Organization, *fakeHookGateway) {
	t.Helper()
	ms := limacharlie.NewMockServer(dummyOpt.OID)
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeHookGateway{t: t, org: org}
	srv := httptest.NewTLSServer(gw)
	t.Cleanup(srv.Close)
	ms.URLs.Hooks = strings.TrimPrefix(srv.URL, "https://")

	// The SDK's webhook sender uses the default transport: trust the fake gateway's certificate.
	origTransport := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	origDelays := adapterResyncRetryDelays
	adapterResyncRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
		adapterResyncRetryDelays = origDelays
	})
	return ms, org, gw
}

func newRotationCLIExtension(t *testing.T, secret string, previous ...string) *CLIExtension {
	t.Helper()
	e := &CLIExtension{
		Name:               "ext-rotation-test",
		SecretKey:          secret,
		PreviousSecretKeys: previous,
		Logger:             dummyLogger{},
		Descriptors:        map[CLIName]CLIDescriptor{"rotation-test-tool": {ProcessCommand: func(context.Context, []string, string) (CLIReturnData, error) { return CLIReturnData{}, nil }}},
	}
	if _, err := e.Init(); err != nil {
		t.Fatal(err)
	}
	return e
}

func adapterSecret(t *testing.T, org *limacharlie.Organization, extName string) interface{} {
	t.Helper()
	rec, err := limacharlie.NewHiveClient(org).Get(limacharlie.HiveArgs{
		HiveName:     "cloud_sensor",
		PartitionKey: org.GetOID(),
		Key:          extName,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec.Data["webhook"].(map[string]interface{})["secret"]
}

// An org that subscribed before the shared secret was rotated has an adapter
// whose secret was derived from the old shared secret. The first hook after
// the rotation is rejected, the adapter is re-synced with the new secret, and
// the hook is delivered.
func TestSendToAdapterResyncsStaleAdapterAfterRotation(t *testing.T) {
	_, org, gw := newRotationFixture(t)

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	oldAdapterSecret := adapterSecret(t, org, oldExt.Name)

	newExt := newRotationCLIExtension(t, "new-secret", "old-secret")
	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)

	if gw.rejected != 1 || gw.accepted != 1 {
		t.Fatalf("gateway rejected %d and accepted %d hooks, want 1 rejected then 1 accepted", gw.rejected, gw.accepted)
	}
	if got := adapterSecret(t, org, newExt.Name); got == oldAdapterSecret {
		t.Fatalf("adapter secret was not re-synced")
	}

	// Later hooks go straight through.
	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)
	if gw.rejected != 1 || gw.accepted != 2 {
		t.Fatalf("gateway rejected %d and accepted %d hooks, want the second hook accepted directly", gw.rejected, gw.accepted)
	}
}

// An adapter that is missing (removed or disabled by the org) is not
// re-created: only a stale secret triggers a re-sync.
func TestSendToAdapterDoesNotRecreateMissingAdapter(t *testing.T) {
	_, org, gw := newRotationFixture(t)
	e := newRotationCLIExtension(t, "new-secret")

	installs := 0
	origInstall := installAdapterFunc
	installAdapterFunc = func(e *CLIExtension, o *limacharlie.Organization) error {
		installs++
		return origInstall(e, o)
	}
	t.Cleanup(func() { installAdapterFunc = origInstall })

	e.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)
	if installs != 0 || gw.rejected != 1 || gw.accepted != 0 {
		t.Fatalf("installs %d, rejected %d, accepted %d: want no re-sync of a missing adapter", installs, gw.rejected, gw.accepted)
	}
}

// When re-syncing the adapter fails, the hook is not retried.
func TestSendToAdapterResyncFailureStopsRetries(t *testing.T) {
	_, org, gw := newRotationFixture(t)

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	newExt := newRotationCLIExtension(t, "new-secret")

	origInstall := installAdapterFunc
	installAdapterFunc = func(*CLIExtension, *limacharlie.Organization) error { return errors.New("permission denied") }
	t.Cleanup(func() { installAdapterFunc = origInstall })

	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)
	if gw.rejected != 1 || gw.accepted != 0 {
		t.Fatalf("rejected %d, accepted %d: want a single attempt", gw.rejected, gw.accepted)
	}
}

// A re-sync that does not take effect is retried a bounded number of times.
func TestSendToAdapterRetriesAreBounded(t *testing.T) {
	_, org, gw := newRotationFixture(t)

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	newExt := newRotationCLIExtension(t, "new-secret")

	origInstall := installAdapterFunc
	installAdapterFunc = func(*CLIExtension, *limacharlie.Organization) error { return nil }
	t.Cleanup(func() { installAdapterFunc = origInstall })

	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)
	if want := 1 + len(adapterResyncRetryDelays); gw.rejected != want || gw.accepted != 0 {
		t.Fatalf("rejected %d, accepted %d: want %d attempts", gw.rejected, gw.accepted, want)
	}
}

// A cancelled request stops waiting for the re-synced adapter.
func TestSendToAdapterStopsRetryingWhenRequestEnds(t *testing.T) {
	_, org, gw := newRotationFixture(t)
	adapterResyncRetryDelays = []time.Duration{time.Hour}

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	newExt := newRotationCLIExtension(t, "new-secret")

	origInstall := installAdapterFunc
	installAdapterFunc = func(*CLIExtension, *limacharlie.Organization) error { return nil }
	t.Cleanup(func() { installAdapterFunc = origInstall })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	newExt.sendToAdapter(ctx, time.Now().Add(time.Hour), org, testHook)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("sendToAdapter took %v after the request ended", elapsed)
	}
	if gw.rejected != 1 || gw.accepted != 0 {
		t.Fatalf("rejected %d, accepted %d: want only the first attempt", gw.rejected, gw.accepted)
	}
}

// While the gateway applies the re-synced adapter it answers 503; the hook is
// retried through that and delivered once the change is live.
func TestSendToAdapterRetriesThroughGatewayRefresh(t *testing.T) {
	_, org, gw := newRotationFixture(t)
	gw.refreshing503 = 2

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	newExt := newRotationCLIExtension(t, "new-secret", "old-secret")
	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)

	if gw.rejected != 1 || gw.unavailable != 2 || gw.accepted != 1 {
		t.Fatalf("rejected %d, unavailable %d, accepted %d: want the hook delivered after the refresh", gw.rejected, gw.unavailable, gw.accepted)
	}
}

// A 503 on the first attempt is not a stale secret: nothing is re-synced.
func TestSendToAdapterDoesNotResyncOnUnavailable(t *testing.T) {
	_, org, _ := newRotationFixture(t)
	e := newRotationCLIExtension(t, "new-secret")
	if err := e.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	installs := 0
	origInstall := installAdapterFunc
	installAdapterFunc = func(*CLIExtension, *limacharlie.Organization) error { installs++; return nil }
	t.Cleanup(func() { installAdapterFunc = origInstall })
	origSend := sendToWebhookAdapterFunc
	sendToWebhookAdapterFunc = func(*core.Extension, *limacharlie.Organization, limacharlie.Dict) error {
		return errors.New(`http status code 503: {"error":"webhook queue full; retry later"}`)
	}
	t.Cleanup(func() { sendToWebhookAdapterFunc = origSend })

	e.sendToAdapter(context.Background(), time.Now().Add(time.Hour), org, testHook)
	if installs != 0 {
		t.Fatalf("re-synced the adapter %d times on a 503", installs)
	}
}

// No retry is started that would end past the request's time budget, so the
// response still reaches the extension manager before it gives up.
func TestSendToAdapterStopsRetryingPastRequestBudget(t *testing.T) {
	_, org, gw := newRotationFixture(t)
	// A retry the budget check misses would hang for an hour, not finish a little late.
	adapterResyncRetryDelays = []time.Duration{time.Hour}

	oldExt := newRotationCLIExtension(t, "old-secret")
	if err := oldExt.installRulesIfNeeded(org); err != nil {
		t.Fatal(err)
	}
	staleSecret := adapterSecret(t, org, oldExt.Name)
	newExt := newRotationCLIExtension(t, "new-secret")

	start := time.Now()
	newExt.sendToAdapter(context.Background(), time.Now().Add(time.Minute), org, testHook)
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("waited %v for a retry that could not finish in time", elapsed)
	}
	if gw.rejected != 1 || gw.accepted != 0 {
		t.Fatalf("rejected %d, accepted %d: want only the first attempt", gw.rejected, gw.accepted)
	}
	// The adapter is still re-synced, so the next command's hook goes through.
	if got := adapterSecret(t, org, newExt.Name); got == staleSecret {
		t.Fatal("adapter was not re-synced")
	}
}
