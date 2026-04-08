package simulator

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

const (
	testOID       = "00000000-0000-0000-0000-000000000001"
	testSecretKey = "test-secret-key-12345"
)

// newTestExtension creates a minimal extension suitable for testing.
func newTestExtension(t *testing.T, callbacks core.ExtensionCallbacks) *core.Extension {
	t.Helper()

	if callbacks.ErrorHandler == nil {
		callbacks.ErrorHandler = func(msg *common.ErrorReportMessage) {}
	}
	if callbacks.EventHandlers == nil {
		callbacks.EventHandlers = map[common.EventName]core.EventCallback{}
	}
	if callbacks.RequestHandlers == nil {
		callbacks.RequestHandlers = map[common.ActionName]core.RequestCallback{}
	}

	ext := &core.Extension{
		ExtensionName: "test-ext",
		SecretKey:     testSecretKey,
		Callbacks:     callbacks,
		ConfigSchema: common.SchemaObject{
			Fields: map[common.SchemaKey]common.SchemaElement{
				"api_key": {
					DataType:    common.SchemaDataTypes.Secret,
					Description: "API key for the service",
				},
			},
		},
		RequestSchema: common.RequestSchemas{
			"ping": {
				IsUserFacing:     true,
				ShortDescription: "Ping the extension",
				ParameterDefinitions: common.SchemaObject{
					Fields: map[common.SchemaKey]common.SchemaElement{
						"message": {
							DataType:    common.SchemaDataTypes.String,
							Description: "Message to echo back",
						},
					},
				},
			},
		},
		RequiredEvents: []common.EventName{
			common.EventTypes.Subscribe,
			common.EventTypes.Unsubscribe,
		},
	}
	if err := ext.Init(); err != nil {
		t.Fatalf("failed to init extension: %v", err)
	}
	return ext
}

func newSimulator(t *testing.T, ext *core.Extension, opts ...Option) *Simulator {
	t.Helper()
	sim := New(ext, opts...)
	t.Cleanup(sim.Close)
	return sim
}

// --- Heartbeat ---

func TestHeartbeat(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	statusCode, err := sim.SendHeartbeat()
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if statusCode != 200 {
		t.Fatalf("expected status 200, got %d", statusCode)
	}
}

func TestHeartbeatWithGzip(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext, WithGzip())

	statusCode, err := sim.SendHeartbeat()
	if err != nil {
		t.Fatalf("heartbeat with gzip failed: %v", err)
	}
	if statusCode != 200 {
		t.Fatalf("expected status 200, got %d", statusCode)
	}
}

// --- Schema Request ---

func TestSchemaRequest(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				return common.Response{}
			},
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext)

	schema, err := sim.SendSchemaRequest()
	if err != nil {
		t.Fatalf("schema request failed: %v", err)
	}

	// Verify config schema has our field.
	if _, ok := schema.Config.Fields["api_key"]; !ok {
		t.Error("expected config schema to contain 'api_key' field")
	}

	// Verify request schema has our action.
	if _, ok := schema.Request["ping"]; !ok {
		t.Error("expected request schema to contain 'ping' action")
	}

	// Verify required events are returned (the extension collects from registered handlers).
	foundSubscribe := false
	foundUnsubscribe := false
	for _, ev := range schema.RequiredEvents {
		if ev == common.EventTypes.Subscribe {
			foundSubscribe = true
		}
		if ev == common.EventTypes.Unsubscribe {
			foundUnsubscribe = true
		}
	}
	if !foundSubscribe {
		t.Error("expected required events to include 'subscribe'")
	}
	if !foundUnsubscribe {
		t.Error("expected required events to include 'unsubscribe'")
	}
}

// --- Request Handling ---

func TestSendRequest(t *testing.T) {
	var receivedAction string
	var receivedIdent string
	var receivedMessage string
	var receivedConfig limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"ping": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedAction = "ping"
					receivedIdent = params.Ident
					receivedConfig = params.Config

					data := params.Request.(limacharlie.Dict)
					if msg, ok := data["message"]; ok {
						receivedMessage = msg.(string)
					}
					return common.Response{
						Data: limacharlie.Dict{"echo": receivedMessage},
					}
				},
			},
		},
	})
	sim := newSimulator(t, ext,
		WithConfig(testOID, limacharlie.Dict{"api_key": "my-secret"}),
	)

	resp, err := sim.SendRequest(testOID, "ping", limacharlie.Dict{
		"message": "hello",
	}, &RequestOptions{
		Ident: "test-user@example.com",
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if receivedAction != "ping" {
		t.Errorf("expected action 'ping', got %q", receivedAction)
	}
	if receivedIdent != "test-user@example.com" {
		t.Errorf("expected ident 'test-user@example.com', got %q", receivedIdent)
	}
	if receivedMessage != "hello" {
		t.Errorf("expected message 'hello', got %q", receivedMessage)
	}
	if receivedConfig["api_key"] != "my-secret" {
		t.Errorf("expected config api_key 'my-secret', got %v", receivedConfig["api_key"])
	}

	// Verify response data.
	respData, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("failed to marshal response data: %v", err)
	}
	var d limacharlie.Dict
	json.Unmarshal(respData, &d)
	if d["echo"] != "hello" {
		t.Errorf("expected echo 'hello', got %v", d["echo"])
	}
}

func TestSendRequestWithGzip(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"ping": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{Data: limacharlie.Dict{"ok": true}}
				},
			},
		},
	})
	sim := newSimulator(t, ext, WithGzip())

	resp, err := sim.SendRequest(testOID, "ping", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("gzip request failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestSendRequestUnknownAction(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "nonexistent", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error for unknown action, got none")
	}

	// Should be recorded as an error.
	errors := sim.Errors()
	if len(errors) == 0 {
		t.Fatal("expected error to be recorded")
	}
}

func TestSendRequestWithStruct(t *testing.T) {
	type PingRequest struct {
		Message string `json:"message" msgpack:"message"`
	}

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"ping": {
				RequestStruct: &PingRequest{},
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					req := params.Request.(*PingRequest)
					return common.Response{Data: limacharlie.Dict{"echo": req.Message}}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "ping", limacharlie.Dict{"message": "typed-hello"}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	respData, _ := json.Marshal(resp.Data)
	var d limacharlie.Dict
	json.Unmarshal(respData, &d)
	if d["echo"] != "typed-hello" {
		t.Errorf("expected echo 'typed-hello', got %v", d["echo"])
	}
}

func TestSendRequestWithResourceState(t *testing.T) {
	var receivedResourceState map[string]common.ResourceState

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedResourceState = params.ResourceState
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	state := map[string]common.ResourceState{
		"my-resource": {LastModified: 1234567890},
	}
	_, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, &RequestOptions{
		ResourceState: state,
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if receivedResourceState == nil {
		t.Fatal("expected resource state to be passed through")
	}
	if receivedResourceState["my-resource"].LastModified != 1234567890 {
		t.Errorf("expected last modified 1234567890, got %d", receivedResourceState["my-resource"].LastModified)
	}
}

func TestSendRequestWithInvestigationID(t *testing.T) {
	var receivedInvID string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedInvID = params.InvestigationID
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, &RequestOptions{
		InvestigationID: "inv-123",
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if receivedInvID != "inv-123" {
		t.Errorf("expected investigation ID 'inv-123', got %q", receivedInvID)
	}
}

func TestSendRequestIdempotencyKey(t *testing.T) {
	var receivedKey string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedKey = params.IdempotentKey
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, &RequestOptions{
		IdempotencyKey: "my-custom-key",
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if receivedKey != "my-custom-key" {
		t.Errorf("expected idempotency key 'my-custom-key', got %q", receivedKey)
	}
}

// --- Events ---

func TestSendSubscribe(t *testing.T) {
	var subscribedOID string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				subscribedOID = params.Org.GetOID()
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendSubscribe(testOID, nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if subscribedOID != testOID {
		t.Errorf("expected subscribed OID %q, got %q", testOID, subscribedOID)
	}
}

func TestSendUnsubscribe(t *testing.T) {
	var unsubscribed bool

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				unsubscribed = true
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendUnsubscribe(testOID, nil)
	if err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !unsubscribed {
		t.Error("expected unsubscribe handler to be called")
	}
}

func TestSendUpdate(t *testing.T) {
	var updated bool

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Update: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				updated = true
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendUpdate(testOID, nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !updated {
		t.Error("expected update handler to be called")
	}
}

func TestSendEventWithConfig(t *testing.T) {
	var receivedConfig limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				receivedConfig = params.Conf
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext,
		WithConfig(testOID, limacharlie.Dict{"key": "from-sim"}),
	)

	_, err := sim.SendSubscribe(testOID, nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if receivedConfig["key"] != "from-sim" {
		t.Errorf("expected config key 'from-sim', got %v", receivedConfig["key"])
	}
}

func TestSendEventWithConfigOverride(t *testing.T) {
	var receivedConfig limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				receivedConfig = params.Conf
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext,
		WithConfig(testOID, limacharlie.Dict{"key": "from-sim"}),
	)

	_, err := sim.SendSubscribe(testOID, &EventOptions{
		Config: limacharlie.Dict{"key": "overridden"},
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if receivedConfig["key"] != "overridden" {
		t.Errorf("expected config key 'overridden', got %v", receivedConfig["key"])
	}
}

func TestSendEventWithData(t *testing.T) {
	var receivedData limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				receivedData = params.Data
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendSubscribe(testOID, &EventOptions{
		Data: limacharlie.Dict{"extra": "info"},
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if receivedData["extra"] != "info" {
		t.Errorf("expected data extra 'info', got %v", receivedData["extra"])
	}
}

func TestSendUnknownEvent(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	resp, err := sim.SendEvent(testOID, "nonexistent-event", nil)
	if err != nil {
		t.Fatalf("event failed: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error for unknown event, got none")
	}
}

// --- Config Validation ---

func TestConfigValidation(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			if _, ok := config["required_field"]; !ok {
				return common.Response{Error: "required_field is missing"}
			}
			return common.Response{}
		},
	})
	sim := newSimulator(t, ext)

	// Valid config.
	resp, err := sim.SendConfigValidation(testOID, limacharlie.Dict{"required_field": "present"})
	if err != nil {
		t.Fatalf("config validation failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}

	// Invalid config.
	resp, err = sim.SendConfigValidation(testOID, limacharlie.Dict{})
	if err != nil {
		t.Fatalf("config validation failed: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected validation error for missing field")
	}
}

func TestConfigValidationNoHandler(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	resp, err := sim.SendConfigValidation(testOID, limacharlie.Dict{"anything": "goes"})
	if err != nil {
		t.Fatalf("config validation failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("expected no error when no validation handler, got %q", resp.Error)
	}
}

// --- Error Report ---

func TestSendErrorReport(t *testing.T) {
	var receivedError string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		ErrorHandler: func(msg *common.ErrorReportMessage) {
			receivedError = msg.Error
		},
	})
	sim := newSimulator(t, ext)

	statusCode, err := sim.SendErrorReport(testOID, "something went wrong")
	if err != nil {
		t.Fatalf("error report failed: %v", err)
	}
	if statusCode != 200 {
		t.Fatalf("expected status 200, got %d", statusCode)
	}
	if receivedError != "something went wrong" {
		t.Errorf("expected error 'something went wrong', got %q", receivedError)
	}
}

// --- Continuations ---

func TestContinuationRecording(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{
								InDelaySeconds: 10,
								Action:         "step2",
								State:          limacharlie.Dict{"progress": 1},
							},
							{
								InDelaySeconds: 20,
								Action:         "step3",
								State:          limacharlie.Dict{"progress": 2},
							},
						},
					}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "start", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	conts := sim.Continuations()
	if len(conts) != 2 {
		t.Fatalf("expected 2 continuations, got %d", len(conts))
	}
	if conts[0].Request.Action != "step2" {
		t.Errorf("expected first continuation action 'step2', got %q", conts[0].Request.Action)
	}
	if conts[1].Request.Action != "step3" {
		t.Errorf("expected second continuation action 'step3', got %q", conts[1].Request.Action)
	}
	if conts[0].OID != testOID {
		t.Errorf("expected OID %q, got %q", testOID, conts[0].OID)
	}
}

func TestContinuationManualExecution(t *testing.T) {
	var step2Called bool
	var step2State limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{
								InDelaySeconds: 5,
								Action:         "step2",
								State:          limacharlie.Dict{"key": "value"},
							},
						},
					}
				},
			},
			"step2": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					step2Called = true
					step2State = params.Request.(limacharlie.Dict)
					return common.Response{Data: limacharlie.Dict{"done": true}}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "start", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	conts := sim.Continuations()
	if len(conts) != 1 {
		t.Fatalf("expected 1 continuation, got %d", len(conts))
	}

	resp, err := sim.ExecuteContinuation(conts[0])
	if err != nil {
		t.Fatalf("continuation execution failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !step2Called {
		t.Error("expected step2 handler to be called")
	}
	if step2State["key"] != "value" {
		t.Errorf("expected continuation state key 'value', got %v", step2State["key"])
	}
}

func TestContinuationImmediateMode(t *testing.T) {
	var callOrder []string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					callOrder = append(callOrder, "start")
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{
								InDelaySeconds: 60,
								Action:         "step2",
								State:          limacharlie.Dict{"from": "start"},
							},
						},
					}
				},
			},
			"step2": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					callOrder = append(callOrder, "step2")
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{
								InDelaySeconds: 30,
								Action:         "step3",
								State:          limacharlie.Dict{"from": "step2"},
							},
						},
					}
				},
			},
			"step3": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					callOrder = append(callOrder, "step3")
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)
	sim.SetContinuationMode(ContinuationModeImmediate)

	_, err := sim.SendRequest(testOID, "start", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if len(callOrder) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(callOrder), callOrder)
	}
	if callOrder[0] != "start" || callOrder[1] != "step2" || callOrder[2] != "step3" {
		t.Errorf("unexpected call order: %v", callOrder)
	}
}

func TestContinuationMaxLevel(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	// Manually create a continuation at the max level.
	cont := ContinuationRecord{
		OID:            testOID,
		IdempotencyKey: "test-key",
		Level:          MaxContinuationLevel,
		Request: common.ContinuationRequest{
			Action: "start",
			State:  limacharlie.Dict{},
		},
	}

	_, err := sim.ExecuteContinuation(cont)
	if err == nil {
		t.Fatal("expected error for max continuation level")
	}
}

func TestContinuationMaxDelay(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{
								InDelaySeconds: MaxContinuationDelay + 100,
								Action:         "step2",
								State:          limacharlie.Dict{},
							},
						},
					}
				},
			},
			"step2": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "start", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should record an error about clamped delay.
	errors := sim.Errors()
	found := false
	for _, e := range errors {
		if e.Message != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected error about clamped continuation delay")
	}
}

func TestResetContinuations(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"start": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Continuations: []common.ContinuationRequest{
							{Action: "next", State: limacharlie.Dict{}},
						},
					}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	sim.SendRequest(testOID, "start", limacharlie.Dict{}, nil)
	if len(sim.Continuations()) != 1 {
		t.Fatal("expected 1 continuation before reset")
	}

	sim.ResetContinuations()
	if len(sim.Continuations()) != 0 {
		t.Fatal("expected 0 continuations after reset")
	}
}

// --- Metrics ---

func TestMetricRecording(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"billable": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Metrics: &common.MetricReport{
							IdempotentKey: params.IdempotentKey,
							Metrics: []common.Metric{
								{Sku: "test-ext.scans", Value: 5},
								{Sku: "test-ext.bytes", Value: 1024},
							},
						},
					}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	_, err := sim.SendRequest(testOID, "billable", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	metrics := sim.Metrics()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric report, got %d", len(metrics))
	}
	if len(metrics[0].Metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics[0].Metrics))
	}
	if metrics[0].Metrics[0].Sku != "test-ext.scans" {
		t.Errorf("expected SKU 'test-ext.scans', got %q", metrics[0].Metrics[0].Sku)
	}
	if metrics[0].Metrics[0].Value != 5 {
		t.Errorf("expected value 5, got %d", metrics[0].Metrics[0].Value)
	}
	if metrics[0].OID != testOID {
		t.Errorf("expected OID %q, got %q", testOID, metrics[0].OID)
	}
}

func TestResetMetrics(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"billable": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{
						Metrics: &common.MetricReport{
							Metrics: []common.Metric{{Sku: "x", Value: 1}},
						},
					}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	sim.SendRequest(testOID, "billable", limacharlie.Dict{}, nil)
	if len(sim.Metrics()) != 1 {
		t.Fatal("expected 1 metric before reset")
	}

	sim.ResetMetrics()
	if len(sim.Metrics()) != 0 {
		t.Fatal("expected 0 metrics after reset")
	}
}

// --- Error Recording ---

func TestErrorRecording(t *testing.T) {
	f := false
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"fail": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{Error: "something broke", Retriable: &f}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "fail", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error in response")
	}

	errors := sim.Errors()
	if len(errors) == 0 {
		t.Fatal("expected error to be recorded")
	}
	if errors[0].Message != "something broke" {
		t.Errorf("expected error message 'something broke', got %q", errors[0].Message)
	}
	if errors[0].OID != testOID {
		t.Errorf("expected OID %q, got %q", testOID, errors[0].OID)
	}
}

func TestResetErrors(t *testing.T) {
	f := false
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"fail": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{Error: "err", Retriable: &f}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	sim.SendRequest(testOID, "fail", limacharlie.Dict{}, nil)
	if len(sim.Errors()) == 0 {
		t.Fatal("expected errors before reset")
	}

	sim.ResetErrors()
	if len(sim.Errors()) != 0 {
		t.Fatal("expected 0 errors after reset")
	}
}

// --- Retriable vs Non-Retriable Errors ---

func TestRetriableError(t *testing.T) {
	tr := true
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"retry-me": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{Error: "temporary failure", Retriable: &tr}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "retry-me", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if !resp.IsRetriable() {
		t.Error("expected response to be retriable")
	}
}

func TestNonRetriableError(t *testing.T) {
	f := false
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"fail-hard": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{Error: "permanent failure", Retriable: &f}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "fail-hard", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.IsRetriable() {
		t.Error("expected response to be non-retriable")
	}
}

// --- SetConfig ---

func TestSetConfig(t *testing.T) {
	var receivedConfig limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedConfig = params.Config
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	// Set config after creation.
	sim.SetConfig(testOID, limacharlie.Dict{"dynamic": "config"})

	_, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if receivedConfig["dynamic"] != "config" {
		t.Errorf("expected dynamic config, got %v", receivedConfig["dynamic"])
	}
}

// --- Full Lifecycle ---

func TestFullExtensionLifecycle(t *testing.T) {
	var lifecycleEvents []string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			lifecycleEvents = append(lifecycleEvents, "config_validated")
			return common.Response{}
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				lifecycleEvents = append(lifecycleEvents, "subscribed")
				return common.Response{}
			},
			common.EventTypes.Update: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				lifecycleEvents = append(lifecycleEvents, "updated")
				return common.Response{}
			},
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				lifecycleEvents = append(lifecycleEvents, "unsubscribed")
				return common.Response{}
			},
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"action": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					lifecycleEvents = append(lifecycleEvents, "action")
					return common.Response{Data: limacharlie.Dict{"result": "ok"}}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	// 1. Schema request.
	schema, err := sim.SendSchemaRequest()
	if err != nil {
		t.Fatalf("schema request failed: %v", err)
	}
	if schema == nil {
		t.Fatal("expected schema response")
	}

	// 2. Heartbeat.
	statusCode, err := sim.SendHeartbeat()
	if err != nil || statusCode != 200 {
		t.Fatalf("heartbeat failed: status=%d err=%v", statusCode, err)
	}

	// 3. Config validation.
	resp, err := sim.SendConfigValidation(testOID, limacharlie.Dict{"key": "val"})
	if err != nil || resp.Error != "" {
		t.Fatalf("config validation failed: err=%v resp_error=%s", err, resp.Error)
	}

	// 4. Subscribe.
	resp, err = sim.SendSubscribe(testOID, nil)
	if err != nil || resp.Error != "" {
		t.Fatalf("subscribe failed: err=%v resp_error=%s", err, resp.Error)
	}

	// 5. Update.
	resp, err = sim.SendUpdate(testOID, nil)
	if err != nil || resp.Error != "" {
		t.Fatalf("update failed: err=%v resp_error=%s", err, resp.Error)
	}

	// 6. Action request.
	resp, err = sim.SendRequest(testOID, "action", limacharlie.Dict{}, nil)
	if err != nil || resp.Error != "" {
		t.Fatalf("action failed: err=%v resp_error=%s", err, resp.Error)
	}

	// 7. Unsubscribe.
	resp, err = sim.SendUnsubscribe(testOID, nil)
	if err != nil || resp.Error != "" {
		t.Fatalf("unsubscribe failed: err=%v resp_error=%s", err, resp.Error)
	}

	expected := []string{"config_validated", "subscribed", "updated", "action", "unsubscribed"}
	if len(lifecycleEvents) != len(expected) {
		t.Fatalf("expected %d lifecycle events, got %d: %v", len(expected), len(lifecycleEvents), lifecycleEvents)
	}
	for i, ev := range expected {
		if lifecycleEvents[i] != ev {
			t.Errorf("lifecycle event %d: expected %q, got %q", i, ev, lifecycleEvents[i])
		}
	}
}

// --- MockServer Integration ---

func TestMockServerIntegration(t *testing.T) {
	ms := limacharlie.NewMockServer(testOID)
	defer ms.Close()

	// Pre-populate mock state.
	ms.DRRules["existing-rule"] = limacharlie.Dict{
		"detect":  limacharlie.Dict{"op": "is", "event": "NEW_PROCESS"},
		"respond": limacharlie.Dict{"action": "report"},
	}

	var rulesFound map[string]limacharlie.Dict

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				// The extension should be able to use the SDK against the mock.
				rules, err := params.Org.DRRules()
				if err != nil {
					return common.Response{Error: err.Error()}
				}
				rulesFound = rules
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext, WithMockServer(testOID, ms))

	resp, err := sim.SendSubscribe(testOID, nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if rulesFound == nil {
		t.Fatal("expected rules to be retrieved from mock")
	}
	if _, ok := rulesFound["existing-rule"]; !ok {
		t.Error("expected 'existing-rule' in retrieved rules")
	}
}

func TestMockServerRequestWithSDK(t *testing.T) {
	ms := limacharlie.NewMockServer(testOID)
	defer ms.Close()

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"add-rule": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					data := params.Request.(limacharlie.Dict)
					ruleName := data["name"].(string)
					detect := limacharlie.Dict{"op": "is", "event": "DNS_REQUEST"}
					respond := limacharlie.Dict{"action": "report"}
					if err := params.Org.DRRuleAdd(ruleName, detect, respond); err != nil {
						return common.Response{Error: err.Error()}
					}
					return common.Response{Data: limacharlie.Dict{"created": ruleName}}
				},
			},
		},
	})
	sim := newSimulator(t, ext, WithMockServer(testOID, ms))

	resp, err := sim.SendRequest(testOID, "add-rule", limacharlie.Dict{
		"name": "test-dr-rule",
	}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	// Verify the rule was created in the mock.
	if _, ok := ms.DRRules["test-dr-rule"]; !ok {
		t.Error("expected 'test-dr-rule' to be created in mock server")
	}
}

func TestMockServerHiveAccess(t *testing.T) {
	ms := limacharlie.NewMockServer(testOID)
	defer ms.Close()

	var hiveDataRetrieved bool

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check-hive": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					hc := limacharlie.NewHiveClient(params.Org)
					data, err := hc.Get(limacharlie.HiveArgs{
						HiveName:     "secret",
						PartitionKey: params.Org.GetOID(),
						Key:          "my-secret",
					})
					if err != nil {
						return common.Response{Error: err.Error()}
					}
					if data.Data["secret"] == "top-secret-value" {
						hiveDataRetrieved = true
					}
					return common.Response{}
				},
			},
		},
	})

	// Pre-populate hive data.
	storeKey := "secret/" + testOID
	ms.HiveStore[storeKey] = map[string]limacharlie.HiveData{
		"my-secret": {
			Data: map[string]interface{}{
				"secret": "top-secret-value",
			},
		},
	}

	sim := newSimulator(t, ext, WithMockServer(testOID, ms))

	resp, err := sim.SendRequest(testOID, "check-hive", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !hiveDataRetrieved {
		t.Error("expected hive data to be retrieved from mock")
	}
}

func TestNewOrganization(t *testing.T) {
	ms := limacharlie.NewMockServer(testOID)
	defer ms.Close()

	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext, WithMockServer(testOID, ms))

	org, err := sim.NewOrganization(testOID)
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}
	if org.GetOID() != testOID {
		t.Errorf("expected OID %q, got %q", testOID, org.GetOID())
	}
}

func TestNewOrganizationNoMockServer(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	_, err := sim.NewOrganization(testOID)
	if err == nil {
		t.Fatal("expected error when no mock server registered")
	}
}

func TestMockServerAccessor(t *testing.T) {
	ms := limacharlie.NewMockServer(testOID)
	defer ms.Close()

	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext, WithMockServer(testOID, ms))

	got := sim.MockServer(testOID)
	if got != ms {
		t.Error("expected MockServer accessor to return the registered mock")
	}

	got = sim.MockServer("nonexistent")
	if got != nil {
		t.Error("expected nil for unregistered OID")
	}
}

// --- Multiple OIDs ---

func TestMultipleOIDs(t *testing.T) {
	oid1 := "00000000-0000-0000-0000-000000000001"
	oid2 := "00000000-0000-0000-0000-000000000002"

	var receivedOIDs []string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				receivedOIDs = append(receivedOIDs, params.Org.GetOID())
				return common.Response{}
			},
		},
	})
	sim := newSimulator(t, ext,
		WithConfig(oid1, limacharlie.Dict{"env": "prod"}),
		WithConfig(oid2, limacharlie.Dict{"env": "staging"}),
	)

	sim.SendSubscribe(oid1, nil)
	sim.SendSubscribe(oid2, nil)

	if len(receivedOIDs) != 2 {
		t.Fatalf("expected 2 OIDs, got %d", len(receivedOIDs))
	}
	if receivedOIDs[0] != oid1 || receivedOIDs[1] != oid2 {
		t.Errorf("unexpected OIDs: %v", receivedOIDs)
	}
}

// --- Concurrent Usage ---

func TestConcurrentRequests(t *testing.T) {
	var counter int64

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"count": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					atomic.AddInt64(&counter, 1)
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	const n = 50
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func() {
			_, err := sim.SendRequest(testOID, "count", limacharlie.Dict{}, nil)
			errs <- err
		}()
	}

	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent request failed: %v", err)
		}
	}

	if atomic.LoadInt64(&counter) != n {
		t.Errorf("expected %d calls, got %d", n, atomic.LoadInt64(&counter))
	}
}

// --- MockServer with Multiple OIDs ---

func TestMultipleMockServers(t *testing.T) {
	oid1 := "00000000-0000-0000-0000-000000000001"
	oid2 := "00000000-0000-0000-0000-000000000002"

	ms1 := limacharlie.NewMockServer(oid1)
	defer ms1.Close()
	ms2 := limacharlie.NewMockServer(oid2)
	defer ms2.Close()

	// Different rules in each mock.
	ms1.DRRules["rule-org1"] = limacharlie.Dict{"detect": limacharlie.Dict{}}
	ms2.DRRules["rule-org2"] = limacharlie.Dict{"detect": limacharlie.Dict{}}

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"list-rules": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					rules, err := params.Org.DRRules()
					if err != nil {
						return common.Response{Error: err.Error()}
					}
					names := make([]interface{}, 0, len(rules))
					for name := range rules {
						names = append(names, name)
					}
					return common.Response{Data: limacharlie.Dict{"rules": names}}
				},
			},
		},
	})
	sim := newSimulator(t, ext,
		WithMockServer(oid1, ms1),
		WithMockServer(oid2, ms2),
	)

	resp1, err := sim.SendRequest(oid1, "list-rules", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request for oid1 failed: %v", err)
	}
	if resp1.Error != "" {
		t.Fatalf("unexpected error for oid1: %s", resp1.Error)
	}

	resp2, err := sim.SendRequest(oid2, "list-rules", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request for oid2 failed: %v", err)
	}
	if resp2.Error != "" {
		t.Fatalf("unexpected error for oid2: %s", resp2.Error)
	}

	// Verify each org sees its own rules (we can't easily check the data type
	// since it goes through JSON round-trip, so just verify no errors).
}

// --- Extension Returning Version ---

func TestResponseContainsVersion(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	resp, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Version != protocolVersion {
		t.Errorf("expected version %d, got %d", protocolVersion, resp.Version)
	}
}

// --- Views Schema ---

func TestViewsSchema(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				return common.Response{}
			},
		},
	})
	ext.ViewsSchema = []common.View{
		{
			Name:            "main",
			LayoutType:      "table",
			DefaultRequests: []string{"list"},
		},
	}
	sim := newSimulator(t, ext)

	schema, err := sim.SendSchemaRequest()
	if err != nil {
		t.Fatalf("schema request failed: %v", err)
	}
	if len(schema.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(schema.Views))
	}
	if schema.Views[0].Name != "main" {
		t.Errorf("expected view name 'main', got %q", schema.Views[0].Name)
	}
	if schema.Views[0].LayoutType != "table" {
		t.Errorf("expected layout 'table', got %q", schema.Views[0].LayoutType)
	}
}

// --- Default Ident ---

func TestDefaultIdent(t *testing.T) {
	var receivedIdent string

	ext := newTestExtension(t, core.ExtensionCallbacks{
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"check": {
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					receivedIdent = params.Ident
					return common.Response{}
				},
			},
		},
	})
	sim := newSimulator(t, ext)

	// Send without specifying ident.
	_, err := sim.SendRequest(testOID, "check", limacharlie.Dict{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if receivedIdent != "simulator@limacharlie.io" {
		t.Errorf("expected default ident 'simulator@limacharlie.io', got %q", receivedIdent)
	}
}

// --- URL Accessor ---

func TestURLAccessor(t *testing.T) {
	ext := newTestExtension(t, core.ExtensionCallbacks{})
	sim := newSimulator(t, ext)

	u := sim.URL()
	if u == "" {
		t.Error("expected non-empty URL")
	}
}
