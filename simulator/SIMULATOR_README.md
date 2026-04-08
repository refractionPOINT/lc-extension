# LimaCharlie Extension Simulator

The `simulator` package lets you test LimaCharlie extensions without a real backend. It simulates the LimaCharlie extension manager — signing requests, dispatching them to your extension, and processing responses — so you can run a full extension lifecycle in a unit test.

When combined with the `MockServer` from `go-limacharlie`, your extension can also make SDK calls (D&R rules, Hive, sensors, etc.) against a simulated API, giving you a complete local test environment.

## Quick Start

```go
package myext_test

import (
    "context"
    "testing"

    lc "github.com/refractionPOINT/go-limacharlie/limacharlie"
    "github.com/refractionPOINT/lc-extension/common"
    "github.com/refractionPOINT/lc-extension/core"
    "github.com/refractionPOINT/lc-extension/simulator"
    "github.com/stretchr/testify/require"
)

func TestPing(t *testing.T) {
    ext := &core.Extension{
        ExtensionName: "my-ext",
        SecretKey:     "test-secret",
        Callbacks: core.ExtensionCallbacks{
            RequestHandlers: map[common.ActionName]core.RequestCallback{
                "ping": {
                    Callback: func(ctx context.Context, p core.RequestCallbackParams) common.Response {
                        data := p.Request.(lc.Dict)
                        return common.Response{Data: lc.Dict{"echo": data["msg"]}}
                    },
                },
            },
            EventHandlers:   map[common.EventName]core.EventCallback{},
            ErrorHandler:    func(e *common.ErrorReportMessage) {},
        },
    }
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    resp, err := sim.SendRequest("test-oid", "ping", lc.Dict{"msg": "hello"}, nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)
}
```

## Core Concepts

### What the Simulator Does

In production, the LimaCharlie backend sends HMAC-signed HTTP webhooks to your extension. The simulator reproduces this exact flow:

1. Serializes a `common.Message` to JSON.
2. Signs it with HMAC-SHA256 using your extension's `SecretKey`.
3. Optionally gzip-compresses the body (just like the real backend).
4. POSTs it to an in-process `httptest.Server` running your extension.
5. Parses the `common.Response` and processes continuations, metrics, and errors.

Your extension code runs unmodified — it receives the same signed webhooks and returns the same responses as it would in production.

### Simulator vs MockServer

These are complementary tools solving different halves of the problem:

| | Simulator | MockServer |
|---|---|---|
| **What it simulates** | The LimaCharlie backend sending webhooks *to* your extension | The LimaCharlie API that your extension calls *out to* |
| **Package** | `lc-extension/simulator` | `go-limacharlie/limacharlie` |
| **Use alone** | Extensions that don't call the LC API during handling | Code that calls the LC SDK directly |
| **Use together** | Full end-to-end: backend webhooks *and* SDK calls in one test | — |

Most real extensions need both. The simulator makes it easy to wire them together.

### Creating a Simulator

```go
ext := &core.Extension{...}
ext.Init()

sim := simulator.New(ext,
    simulator.WithGzip(),                                       // Enable gzip (matches production)
    simulator.WithConfig("oid-1", lc.Dict{"api_key": "xxx"}),  // Pre-set org config
    simulator.WithMockServer("oid-1", ms),                      // Wire up a MockServer
)
defer sim.Close()
```

## How-To Guides

### Testing the Full Extension Lifecycle

Every extension goes through the same lifecycle: schema request, config validation, subscribe, requests, update, unsubscribe. Test the entire flow:

```go
func TestLifecycle(t *testing.T) {
    ext := buildMyExtension() // Your extension factory
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    // 1. LimaCharlie fetches the schema (happens when extension is registered)
    schema, err := sim.SendSchemaRequest()
    require.NoError(t, err)
    require.Contains(t, schema.Request, "scan")  // Verify your actions are advertised

    // 2. Heartbeat (periodic availability check)
    status, err := sim.SendHeartbeat()
    require.NoError(t, err)
    require.Equal(t, 200, status)

    // 3. User configures the extension — backend asks you to validate
    resp, err := sim.SendConfigValidation("oid-1", lc.Dict{"api_key": "valid-key"})
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // 4. Organization subscribes
    resp, err = sim.SendSubscribe("oid-1", nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // 5. User performs an action
    resp, err = sim.SendRequest("oid-1", "scan", lc.Dict{"target": "1.2.3.4"}, nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // 6. Config or rules changed — backend sends update event
    resp, err = sim.SendUpdate("oid-1", nil)
    require.NoError(t, err)

    // 7. Organization unsubscribes
    resp, err = sim.SendUnsubscribe("oid-1", nil)
    require.NoError(t, err)
}
```

### Testing with Organization Config

Extensions receive configuration with every request and event. Set it globally or per-call:

```go
func TestConfigHandling(t *testing.T) {
    ext := buildMyExtension()
    ext.Init()

    // Set config at simulator level — included in all messages for this OID
    sim := simulator.New(ext,
        simulator.WithConfig("oid-1", lc.Dict{
            "api_key":    "my-api-key",
            "threshold":  80,
            "debug_mode": true,
        }),
    )
    defer sim.Close()

    // This request receives the config above
    resp, err := sim.SendRequest("oid-1", "analyze", lc.Dict{}, nil)
    require.NoError(t, err)

    // Override config for a specific call
    resp, err = sim.SendRequest("oid-1", "analyze", lc.Dict{}, &simulator.RequestOptions{
        Config: lc.Dict{"api_key": "different-key"},
    })
    require.NoError(t, err)

    // Update config dynamically mid-test
    sim.SetConfig("oid-1", lc.Dict{"api_key": "rotated-key"})
}
```

### Testing with the LimaCharlie SDK (MockServer Integration)

When your extension makes SDK calls during handling (e.g., creating D&R rules on subscribe), you need both the simulator and a MockServer:

```go
func TestSubscribeCreatesRules(t *testing.T) {
    ms := lc.NewMockServer("oid-1")
    defer ms.Close()

    ext := &core.Extension{
        ExtensionName: "my-detection-ext",
        SecretKey:     "test-secret",
        Callbacks: core.ExtensionCallbacks{
            EventHandlers: map[common.EventName]core.EventCallback{
                common.EventTypes.Subscribe: func(ctx context.Context, p core.EventCallbackParams) common.Response {
                    // Extension creates D&R rules when an org subscribes
                    detect := lc.Dict{"event": "NEW_PROCESS", "op": "is"}
                    respond := lc.Dict{"action": "report", "name": "my-detection"}
                    if err := p.Org.DRRuleAdd("my-rule", detect, respond); err != nil {
                        return common.Response{Error: err.Error()}
                    }
                    return common.Response{}
                },
                common.EventTypes.Unsubscribe: func(ctx context.Context, p core.EventCallbackParams) common.Response {
                    p.Org.DRRuleDelete("my-rule")
                    return common.Response{}
                },
            },
            ErrorHandler: func(e *common.ErrorReportMessage) {},
        },
    }
    ext.Init()

    sim := simulator.New(ext, simulator.WithMockServer("oid-1", ms))
    defer sim.Close()

    // Subscribe — extension creates the rule via the SDK
    resp, err := sim.SendSubscribe("oid-1", nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // Verify the rule was created in the mock
    require.Contains(t, ms.DRRules, "my-rule")

    // Unsubscribe — extension cleans up
    resp, err = sim.SendUnsubscribe("oid-1", nil)
    require.NoError(t, err)
    require.NotContains(t, ms.DRRules, "my-rule")
}
```

### Testing Hive / Secret Access

Extensions that use Hive for secrets or configuration storage:

```go
func TestSecretAccess(t *testing.T) {
    ms := lc.NewMockServer("oid-1")
    defer ms.Close()

    // Pre-populate the secret in the mock's Hive store
    ms.HiveStore["secret/oid-1"] = map[string]lc.HiveData{
        "vendor-api-key": {
            Data: map[string]interface{}{"secret": "sk-live-12345"},
        },
    }

    ext := buildMyExtension() // Extension that reads secrets from Hive
    ext.Init()

    sim := simulator.New(ext, simulator.WithMockServer("oid-1", ms))
    defer sim.Close()

    // Extension can now read secrets via the SDK
    resp, err := sim.SendRequest("oid-1", "call-vendor", lc.Dict{}, nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)
}
```

### Testing Multi-Step Workflows (Continuations)

Extensions can request follow-up calls via continuations. The simulator captures them for inspection, or executes them immediately for testing chains:

```go
func TestContinuationWorkflow(t *testing.T) {
    ext := &core.Extension{
        ExtensionName: "scan-ext",
        SecretKey:     "test-secret",
        Callbacks: core.ExtensionCallbacks{
            RequestHandlers: map[common.ActionName]core.RequestCallback{
                "scan": {
                    Callback: func(ctx context.Context, p core.RequestCallbackParams) common.Response {
                        return common.Response{
                            Data: lc.Dict{"status": "scanning"},
                            Continuations: []common.ContinuationRequest{
                                {
                                    InDelaySeconds: 30,
                                    Action:         "check-results",
                                    State:          lc.Dict{"scan_id": "abc-123"},
                                },
                            },
                        }
                    },
                },
                "check-results": {
                    Callback: func(ctx context.Context, p core.RequestCallbackParams) common.Response {
                        state := p.Request.(lc.Dict)
                        return common.Response{
                            Data: lc.Dict{"scan_id": state["scan_id"], "status": "complete"},
                        }
                    },
                },
            },
            EventHandlers: map[common.EventName]core.EventCallback{},
            ErrorHandler:  func(e *common.ErrorReportMessage) {},
        },
    }
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    // --- Option A: Inspect continuations manually ---

    resp, err := sim.SendRequest("oid-1", "scan", lc.Dict{}, nil)
    require.NoError(t, err)

    conts := sim.Continuations()
    require.Len(t, conts, 1)
    require.Equal(t, "check-results", conts[0].Request.Action)
    require.Equal(t, uint64(30), conts[0].Request.InDelaySeconds)

    // Execute it manually (ignores the delay)
    resp, err = sim.ExecuteContinuation(conts[0])
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // --- Option B: Auto-execute the whole chain ---

    sim.ResetContinuations()
    sim.SetContinuationMode(simulator.ContinuationModeImmediate)

    resp, err = sim.SendRequest("oid-1", "scan", lc.Dict{}, nil)
    require.NoError(t, err)
    // Both "scan" and "check-results" have now been called synchronously
    require.Len(t, sim.Continuations(), 1) // Still recorded for inspection
}
```

### Testing Config Validation

Verify your extension properly rejects invalid configurations:

```go
func TestConfigValidation(t *testing.T) {
    ext := &core.Extension{
        ExtensionName: "my-ext",
        SecretKey:     "test-secret",
        Callbacks: core.ExtensionCallbacks{
            ValidateConfig: func(ctx context.Context, org *lc.Organization, config lc.Dict) common.Response {
                if _, ok := config["api_key"]; !ok {
                    f := false
                    return common.Response{Error: "api_key is required", Retriable: &f}
                }
                return common.Response{}
            },
            EventHandlers: map[common.EventName]core.EventCallback{},
            ErrorHandler:  func(e *common.ErrorReportMessage) {},
        },
    }
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    // Valid config
    resp, err := sim.SendConfigValidation("oid-1", lc.Dict{"api_key": "xxx"})
    require.NoError(t, err)
    require.Empty(t, resp.Error)

    // Invalid config
    resp, err = sim.SendConfigValidation("oid-1", lc.Dict{})
    require.NoError(t, err)
    require.Contains(t, resp.Error, "api_key is required")
}
```

### Testing Error Classification (Retriable vs Permanent)

The backend retries 500 errors but stops on 400 errors. Verify your extension classifies errors correctly:

```go
func TestErrorClassification(t *testing.T) {
    ext := buildMyExtension()
    ext.Init()
    sim := simulator.New(ext)
    defer sim.Close()

    // Test a retriable error (default: Retriable=nil means retriable)
    res, err := sim.SendRequestFull("oid-1", "call-vendor", lc.Dict{}, nil)
    require.NoError(t, err)
    if res.Response.Error != "" {
        // Retriable errors get HTTP 500
        if res.Response.IsRetriable() {
            require.Equal(t, 500, res.StatusCode)
        } else {
            // Permanent errors get HTTP 400
            require.Equal(t, 400, res.StatusCode)
        }
    }
}
```

### Testing Metrics / Billing

Verify your extension reports the right billing metrics:

```go
func TestMetricReporting(t *testing.T) {
    ext := buildBillableExtension()
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    _, err := sim.SendRequest("oid-1", "scan-file", lc.Dict{"path": "/tmp/sample"}, nil)
    require.NoError(t, err)

    metrics := sim.Metrics()
    require.Len(t, metrics, 1)
    require.Equal(t, "oid-1", metrics[0].OID)

    // Verify specific SKUs
    skus := map[string]uint64{}
    for _, m := range metrics[0].Metrics {
        skus[m.Sku] = m.Value
    }
    require.Equal(t, uint64(1), skus["my-ext.scans"])
    require.Greater(t, skus["my-ext.bytes_scanned"], uint64(0))
}
```

### Testing Multi-Tenant (Multiple OIDs)

Simulate multiple organizations subscribing to your extension:

```go
func TestMultiTenant(t *testing.T) {
    ms1 := lc.NewMockServer("oid-1")
    ms2 := lc.NewMockServer("oid-2")
    defer ms1.Close()
    defer ms2.Close()

    ext := buildMyExtension()
    ext.Init()

    sim := simulator.New(ext,
        simulator.WithMockServer("oid-1", ms1),
        simulator.WithMockServer("oid-2", ms2),
        simulator.WithConfig("oid-1", lc.Dict{"env": "prod"}),
        simulator.WithConfig("oid-2", lc.Dict{"env": "staging"}),
    )
    defer sim.Close()

    // Subscribe both orgs
    sim.SendSubscribe("oid-1", nil)
    sim.SendSubscribe("oid-2", nil)

    // Each org has isolated state in its own MockServer
    ms1.DRRules["rule-a"] = lc.Dict{}
    ms2.DRRules["rule-b"] = lc.Dict{}

    // Request for org-1 only sees org-1's rules
    resp, _ := sim.SendRequest("oid-1", "list-rules", lc.Dict{}, nil)
    require.Empty(t, resp.Error)
}
```

### Testing Signature Rejection

Verify your extension rejects requests with invalid signatures:

```go
func TestSignatureRejection(t *testing.T) {
    ext := buildMyExtension()
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    statusCode, err := sim.SendWithBadSignature()
    require.NoError(t, err)
    require.Equal(t, 401, statusCode)
}
```

### Testing Edge Cases with Raw Messages

Send arbitrary messages to test how your extension handles unusual inputs:

```go
func TestEmptyMessage(t *testing.T) {
    ext := buildMyExtension()
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    // Message with no type set — extension should return 400
    msg := &common.Message{
        Version:        20221218,
        IdempotencyKey: "test",
    }
    _, statusCode, err := sim.SendRawMessage(msg)
    require.NoError(t, err)
    require.Equal(t, 400, statusCode)
}
```

### Adding Mock Servers Dynamically

If you need to register mock servers after the simulator is created (e.g., in sub-tests):

```go
func TestDynamicMockRegistration(t *testing.T) {
    ext := buildMyExtension()
    ext.Init()

    sim := simulator.New(ext)
    defer sim.Close()

    // Register a mock server for a new OID later
    ms := lc.NewMockServer("oid-new")
    defer ms.Close()

    sim.AddMockServer("oid-new", ms)

    // Extension can now make SDK calls against this OID
    resp, err := sim.SendSubscribe("oid-new", nil)
    require.NoError(t, err)
    require.Empty(t, resp.Error)
}
```

### Accessing the Mock Directly

Use `sim.MockServer(oid)` or `sim.NewOrganization(oid)` to interact with the mock outside of extension handlers — for setup, verification, or driving parallel SDK operations:

```go
func TestDirectMockAccess(t *testing.T) {
    ms := lc.NewMockServer("oid-1")
    defer ms.Close()

    ext := buildMyExtension()
    ext.Init()

    sim := simulator.New(ext, simulator.WithMockServer("oid-1", ms))
    defer sim.Close()

    // Pre-populate state before the test
    ms.SensorStore["sid-abc"] = &lc.Sensor{
        OID: "oid-1", SID: "sid-abc", Hostname: "web-01",
    }
    ms.SensorOnline["sid-abc"] = true

    // Run your extension action
    sim.SendRequest("oid-1", "check-sensor", lc.Dict{"sid": "sid-abc"}, nil)

    // Use sim.NewOrganization to verify state outside of handlers
    org, err := sim.NewOrganization("oid-1")
    require.NoError(t, err)

    rules, _ := org.DRRules()
    // ... verify whatever the extension should have done
    _ = rules

    // Or access the mock directly
    require.Equal(t, ms, sim.MockServer("oid-1"))
}
```

## API Reference

### Constructor & Options

| Function | Description |
|----------|-------------|
| `New(ext, ...opts)` | Creates a simulator. Starts an httptest.Server hosting the extension. |
| `WithGzip()` | Enable gzip compression on all requests (matches production). |
| `WithConfig(oid, dict)` | Set the extension config for an OID. |
| `WithMockServer(oid, ms)` | Wire up a `limacharlie.MockServer` for an OID. |
| `WithMaxContinuationsPerResponse(n)` | Override the fan-out limit (default: 100, -1 to disable). |

### Sending Messages

| Method | Description |
|--------|-------------|
| `SendHeartbeat()` | Send a heartbeat. Returns `(statusCode, error)`. |
| `SendSchemaRequest()` | Fetch the extension's schema. Returns the parsed `SchemaRequestResponse`. |
| `SendRequest(oid, action, data, opts)` | Send an action request. Returns the `Response`. |
| `SendRequestFull(oid, action, data, opts)` | Like `SendRequest` but also returns the HTTP status code. |
| `SendEvent(oid, eventName, opts)` | Send a custom event. Returns the `Response`. |
| `SendEventFull(oid, eventName, opts)` | Like `SendEvent` but also returns the HTTP status code. |
| `SendSubscribe(oid, opts)` | Convenience for `SendEvent` with event name `"subscribe"`. |
| `SendUnsubscribe(oid, opts)` | Convenience for `SendEvent` with event name `"unsubscribe"`. |
| `SendUpdate(oid, opts)` | Convenience for `SendEvent` with event name `"update"`. |
| `SendConfigValidation(oid, config)` | Send a config validation request. |
| `SendErrorReport(oid, msg)` | Send an error report to the extension's error handler. |
| `SendRawMessage(msg)` | Send an arbitrary `common.Message`. Returns `(body, statusCode, error)`. |
| `SendWithBadSignature()` | Send a message with a wrong HMAC signature. Returns `(statusCode, error)`. |

### Request & Event Options

```go
// RequestOptions
&simulator.RequestOptions{
    Ident:           "user@example.com",        // Override the user identity
    IdempotencyKey:  "custom-key",              // Override the idempotency key
    Config:          lc.Dict{...},              // Override config for this call only
    ResourceState:   map[string]ResourceState{}, // Resource state for Yara/integrity
    InvestigationID: "inv-123",                 // Investigation context
}

// EventOptions
&simulator.EventOptions{
    Ident:          "user@example.com",  // Override the user identity
    Config:         lc.Dict{...},        // Override config for this call only
    Data:           lc.Dict{...},        // Event-specific data
    IdempotencyKey: "custom-key",        // Override the idempotency key
}
```

### Inspecting Results

| Method | Description |
|--------|-------------|
| `Errors()` | Returns all recorded `ErrorRecord`s (from extension responses and simulator-detected issues). |
| `ResetErrors()` | Clears recorded errors. |
| `Metrics()` | Returns all recorded `MetricRecord`s from extension responses. |
| `ResetMetrics()` | Clears recorded metrics. |
| `Continuations()` | Returns all recorded `ContinuationRecord`s. |
| `ResetContinuations()` | Clears recorded continuations. |

### Continuations

| Method | Description |
|--------|-------------|
| `SetContinuationMode(mode)` | `ContinuationModeRecord` (default): record only. `ContinuationModeImmediate`: execute synchronously. |
| `ExecuteContinuation(cont)` | Manually execute a recorded continuation. Returns the `Response`. |

### MockServer Integration

| Method | Description |
|--------|-------------|
| `AddMockServer(oid, ms)` | Register a MockServer for an OID after construction. |
| `MockServer(oid)` | Get the MockServer for an OID (or nil). |
| `NewOrganization(oid)` | Create an `Organization` backed by the mock (for setup/verification outside handlers). |

### Other

| Method | Description |
|--------|-------------|
| `URL()` | Returns the test server's URL. |
| `SetConfig(oid, dict)` | Update the config for an OID after construction. |
| `Close()` | Shut down the test server and clean up. |

## Backend Behavior Simulated

The simulator reproduces these backend behaviors:

| Behavior | How it's simulated |
|----------|-------------------|
| **HMAC-SHA256 signing** | Every request is signed with the extension's `SecretKey`, exactly matching the backend's `lc-ext-sig` header format. |
| **Gzip compression** | Enabled via `WithGzip()`. The backend always compresses; the extension handles both. |
| **Protocol version** | Set to `20221218` in every message, matching the current backend version. |
| **Continuation processing** | Respects max level (10), max delay (300s), and fan-out limits (100 per response). |
| **Idempotency keys** | Generated automatically (or overridden via options). Continuation keys follow the backend's `"parentKey:level"` format. |
| **Metric recording** | Captured for test inspection. In production these drive billing. |
| **Error recording** | Captured from both extension responses and simulator-detected issues (max level exceeded, delay clamped, etc.). |
| **Config injection** | Config is included in request and event messages, matching how the backend fetches and attaches org config. |
| **Org credentials** | Each message includes OID, JWT, and ident, matching the backend's `OrgAccessData` format. |
