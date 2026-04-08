// Package simulator provides a mock LimaCharlie backend for testing extensions.
//
// It acts as the extension manager, sending signed webhook requests to your
// extension under test and processing responses, including continuations.
//
// The simulator can optionally integrate with [limacharlie.MockServer] from
// the go-limacharlie SDK so that extensions which call back into the
// LimaCharlie API during handling also hit a simulated API.
//
// Basic usage:
//
//	ext := &core.Extension{
//	    ExtensionName: "my-ext",
//	    SecretKey:     "test-secret",
//	    Callbacks:     core.ExtensionCallbacks{...},
//	}
//	ext.Init()
//
//	sim := simulator.New(ext)
//	defer sim.Close()
//
//	resp, err := sim.SendRequest("test-oid", "my-action", limacharlie.Dict{"key": "val"}, nil)
package simulator

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

const protocolVersion = 20221218

// Continuation limits matching the real backend.
const (
	MaxContinuationDelay          = 300
	MaxContinuationLevel          = 10
	DefaultMaxContinuationsPerReq = 100
)

// idempotencyCounter provides unique idempotency keys across concurrent callers.
var idempotencyCounter uint64

// ContinuationRecord captures a continuation that was requested by the
// extension. In test mode continuations are not executed automatically unless
// [Simulator.SetContinuationMode] is set to [ContinuationModeImmediate].
type ContinuationRecord struct {
	OID            string
	IdempotencyKey string
	Level          uint64
	Request        common.ContinuationRequest
	QueuedAt       time.Time
}

// ContinuationMode controls how continuations are processed.
type ContinuationMode int

const (
	// ContinuationModeRecord records continuations without executing them.
	// They can be inspected via [Simulator.Continuations] and manually
	// executed with [Simulator.ExecuteContinuation].
	ContinuationModeRecord ContinuationMode = iota

	// ContinuationModeImmediate executes continuations synchronously and
	// immediately (ignoring any delay), which is useful for testing
	// multi-step workflows.
	ContinuationModeImmediate
)

// ErrorRecord captures an error that was reported by the extension or detected
// by the simulator during request processing.
type ErrorRecord struct {
	OID     string
	Message string
	Time    time.Time
}

// MetricRecord captures metrics reported by the extension.
type MetricRecord struct {
	OID            string
	IdempotencyKey string
	Metrics        []common.Metric
	Time           time.Time
}

// Simulator simulates the LimaCharlie backend extension manager for testing.
// It starts an HTTP test server that hosts the extension, then provides
// methods to send each type of backend message (heartbeat, schema request,
// config validation, event, request, error report) to the extension.
type Simulator struct {
	ext    *core.Extension
	server *httptest.Server

	mu               sync.RWMutex
	continuationMode ContinuationMode
	continuations    []ContinuationRecord
	errors           []ErrorRecord
	metrics          []MetricRecord
	configs          map[string]limacharlie.Dict // oid -> config

	// mockServers maps OID to a go-limacharlie MockServer.
	// When set, the simulator will use the mock server to create
	// Organization instances (with working JWT) for the given OID,
	// instead of using dummy credentials.
	mockServers map[string]*limacharlie.MockServer

	// useGzip controls whether requests are gzip-compressed.
	useGzip bool

	// maxContinuationsPerResponse limits how many continuations a single
	// response can generate, matching the backend's fan-out protection.
	// 0 means use DefaultMaxContinuationsPerReq.
	maxContinuationsPerResponse int
}

// Option configures a Simulator.
type Option func(*Simulator)

// WithGzip enables gzip compression of request bodies, matching the
// production backend behavior.
func WithGzip() Option {
	return func(s *Simulator) {
		s.useGzip = true
	}
}

// WithMockServer associates a [limacharlie.MockServer] with an OID. When the
// simulator sends messages for that OID, the JWT and org credentials will come
// from the mock server, and the extension will be able to make real SDK calls
// against the mock.
func WithMockServer(oid string, ms *limacharlie.MockServer) Option {
	return func(s *Simulator) {
		s.mockServers[oid] = ms
	}
}

// WithConfig sets the extension configuration that will be included in request
// and event messages for the given OID.
func WithConfig(oid string, config limacharlie.Dict) Option {
	return func(s *Simulator) {
		s.configs[oid] = config
	}
}

// WithMaxContinuationsPerResponse overrides the maximum number of continuation
// requests that a single response can generate. The default is
// [DefaultMaxContinuationsPerReq] (100). Set to -1 to disable the limit.
func WithMaxContinuationsPerResponse(max int) Option {
	return func(s *Simulator) {
		s.maxContinuationsPerResponse = max
	}
}

// New creates a Simulator for the given extension. The extension's Init()
// must have been called before passing it here. The simulator starts an
// httptest.Server that hosts the extension and must be closed with Close().
func New(ext *core.Extension, opts ...Option) *Simulator {
	s := &Simulator{
		ext:         ext,
		configs:     map[string]limacharlie.Dict{},
		mockServers: map[string]*limacharlie.MockServer{},
	}
	for _, opt := range opts {
		opt(s)
	}

	// Wire up OrgFromAccess so the extension creates Organizations backed
	// by the mock server when one is registered for the given OID.
	// The closure captures s.mockServers (a map reference), so mock servers
	// added later via AddMockServer are automatically picked up.
	ext.OrgFromAccess = func(oad common.OrgAccessData) (*limacharlie.Organization, error) {
		s.mu.RLock()
		ms, ok := s.mockServers[oad.OID]
		s.mu.RUnlock()
		if ok {
			return ms.NewOrganization()
		}
		// Fall back to default behavior.
		return limacharlie.NewOrganizationFromClientOptions(limacharlie.ClientOptions{
			OID: oad.OID,
			JWT: oad.JWT,
		}, nil)
	}

	s.server = httptest.NewServer(ext)
	return s
}

// Close shuts down the test server and cleans up the OrgFromAccess hook.
func (s *Simulator) Close() {
	s.server.Close()
	s.ext.OrgFromAccess = nil
}

// URL returns the test server's URL.
func (s *Simulator) URL() string {
	return s.server.URL
}

// SetContinuationMode controls how continuations are handled.
func (s *Simulator) SetContinuationMode(mode ContinuationMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continuationMode = mode
}

// SetConfig sets or updates the extension configuration for an OID.
func (s *Simulator) SetConfig(oid string, config limacharlie.Dict) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[oid] = config
}

// AddMockServer registers a [limacharlie.MockServer] for the given OID after
// the simulator has been created. This can be called at any time.
func (s *Simulator) AddMockServer(oid string, ms *limacharlie.MockServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mockServers[oid] = ms
}

// Continuations returns all recorded continuation requests.
func (s *Simulator) Continuations() []ContinuationRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ContinuationRecord, len(s.continuations))
	copy(out, s.continuations)
	return out
}

// ResetContinuations clears the recorded continuations.
func (s *Simulator) ResetContinuations() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continuations = nil
}

// Errors returns all recorded errors.
func (s *Simulator) Errors() []ErrorRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ErrorRecord, len(s.errors))
	copy(out, s.errors)
	return out
}

// ResetErrors clears the recorded errors.
func (s *Simulator) ResetErrors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = nil
}

// Metrics returns all recorded metric reports.
func (s *Simulator) Metrics() []MetricRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MetricRecord, len(s.metrics))
	copy(out, s.metrics)
	return out
}

// ResetMetrics clears the recorded metrics.
func (s *Simulator) ResetMetrics() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = nil
}

// --- Message senders ---

// SendHeartbeat sends a heartbeat message and returns the raw HTTP status code.
func (s *Simulator) SendHeartbeat() (int, error) {
	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: generateIdempotencyKey(),
		HeartBeat:      &common.HeartBeatMessage{},
	}
	_, statusCode, err := s.sendRaw(&msg)
	return statusCode, err
}

// SendSchemaRequest sends a schema request and returns the parsed schema response.
func (s *Simulator) SendSchemaRequest() (*common.SchemaRequestResponse, error) {
	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: generateIdempotencyKey(),
		SchemaRequest:  &common.SchemaRequestMessage{},
	}
	resp, _, err := s.sendAndParse(&msg)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("extension error: %s", resp.Error)
	}

	// The response Data should be a SchemaRequestResponse.
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema data: %w", err)
	}
	var schema common.SchemaRequestResponse
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema response: %w", err)
	}
	return &schema, nil
}

// RequestOptions provides optional parameters for SendRequest.
type RequestOptions struct {
	Ident           string
	IdempotencyKey  string
	Config          limacharlie.Dict
	ResourceState   map[string]common.ResourceState
	InvestigationID string
}

// RequestResult holds the full result of a request, including the HTTP status code.
type RequestResult struct {
	Response   *common.Response
	StatusCode int
}

// SendRequest sends an action request to the extension and returns the response.
// The oid identifies the organization. The config stored via WithConfig or SetConfig
// is used unless overridden in opts.
func (s *Simulator) SendRequest(oid string, action string, data limacharlie.Dict, opts *RequestOptions) (*common.Response, error) {
	res, err := s.SendRequestFull(oid, action, data, opts)
	if err != nil {
		return nil, err
	}
	return res.Response, nil
}

// SendRequestFull is like SendRequest but also returns the HTTP status code,
// which is useful for verifying error classification (400 = non-retriable,
// 500 = retriable).
func (s *Simulator) SendRequestFull(oid string, action string, data limacharlie.Dict, opts *RequestOptions) (*RequestResult, error) {
	if opts == nil {
		opts = &RequestOptions{}
	}

	config := s.getConfig(oid)
	if opts.Config != nil {
		config = opts.Config
	}

	idempotencyKey := opts.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = generateIdempotencyKey()
	}

	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: idempotencyKey,
		Request: &common.RequestMessage{
			Org:             s.makeOrgAccess(oid, opts.Ident),
			Action:          action,
			Data:            data,
			Config:          config,
			ResourceState:   opts.ResourceState,
			InvestigationID: opts.InvestigationID,
		},
	}

	resp, statusCode, err := s.sendAndParse(&msg)
	if err != nil {
		return nil, err
	}
	s.processResponse(oid, idempotencyKey, 0, resp)
	return &RequestResult{Response: resp, StatusCode: statusCode}, nil
}

// EventOptions provides optional parameters for SendEvent.
type EventOptions struct {
	Ident          string
	Config         limacharlie.Dict
	Data           limacharlie.Dict
	IdempotencyKey string
}

// EventResult holds the full result of an event, including the HTTP status code.
type EventResult struct {
	Response   *common.Response
	StatusCode int
}

// SendEvent sends an event message (subscribe, unsubscribe, update, or custom)
// to the extension and returns the response.
func (s *Simulator) SendEvent(oid string, eventName string, opts *EventOptions) (*common.Response, error) {
	res, err := s.SendEventFull(oid, eventName, opts)
	if err != nil {
		return nil, err
	}
	return res.Response, nil
}

// SendEventFull is like SendEvent but also returns the HTTP status code.
func (s *Simulator) SendEventFull(oid string, eventName string, opts *EventOptions) (*EventResult, error) {
	if opts == nil {
		opts = &EventOptions{}
	}

	config := s.getConfig(oid)
	if opts.Config != nil {
		config = opts.Config
	}

	data := opts.Data
	if data == nil {
		data = limacharlie.Dict{}
	}

	idempotencyKey := opts.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = generateIdempotencyKey()
	}

	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: idempotencyKey,
		Event: &common.EventMessage{
			Org:       s.makeOrgAccess(oid, opts.Ident),
			EventName: eventName,
			Data:      data,
			Config:    config,
		},
	}

	resp, statusCode, err := s.sendAndParse(&msg)
	if err != nil {
		return nil, err
	}
	s.processResponse(oid, idempotencyKey, 0, resp)
	return &EventResult{Response: resp, StatusCode: statusCode}, nil
}

// SendSubscribe is a convenience method that sends a "subscribe" event.
func (s *Simulator) SendSubscribe(oid string, opts *EventOptions) (*common.Response, error) {
	return s.SendEvent(oid, common.EventTypes.Subscribe, opts)
}

// SendUnsubscribe is a convenience method that sends an "unsubscribe" event.
func (s *Simulator) SendUnsubscribe(oid string, opts *EventOptions) (*common.Response, error) {
	return s.SendEvent(oid, common.EventTypes.Unsubscribe, opts)
}

// SendUpdate is a convenience method that sends an "update" event.
func (s *Simulator) SendUpdate(oid string, opts *EventOptions) (*common.Response, error) {
	return s.SendEvent(oid, common.EventTypes.Update, opts)
}

// SendConfigValidation sends a config validation message and returns the response.
func (s *Simulator) SendConfigValidation(oid string, config limacharlie.Dict) (*common.Response, error) {
	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: generateIdempotencyKey(),
		ConfigValidation: &common.ConfigValidationMessage{
			Org:    s.makeOrgAccess(oid, ""),
			Config: config,
		},
	}

	resp, _, err := s.sendAndParse(&msg)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// SendErrorReport sends an error report message to the extension.
func (s *Simulator) SendErrorReport(oid string, errorMsg string) (int, error) {
	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: generateIdempotencyKey(),
		ErrorReport: &common.ErrorReportMessage{
			Error: errorMsg,
			Oid:   oid,
		},
	}
	_, statusCode, err := s.sendRaw(&msg)
	return statusCode, err
}

// SendRawMessage sends an arbitrary [common.Message] to the extension,
// properly signed and optionally gzipped. This is useful for testing edge
// cases like empty messages or unusual field combinations.
func (s *Simulator) SendRawMessage(msg *common.Message) ([]byte, int, error) {
	return s.sendRaw(msg)
}

// SendWithBadSignature sends a message with an intentionally wrong HMAC
// signature, for testing that the extension correctly rejects it.
func (s *Simulator) SendWithBadSignature() (int, error) {
	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: generateIdempotencyKey(),
		HeartBeat:      &common.HeartBeatMessage{},
	}
	payload, err := json.Marshal(&msg)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.server.URL, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("lc-ext-sig", "0000000000000000000000000000000000000000000000000000000000000000")

	resp, err := s.server.Client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	return resp.StatusCode, nil
}

// ExecuteContinuation manually executes a recorded continuation. It sends the
// continuation as a new request to the extension at the appropriate continuation
// level. Returns the response from the extension.
func (s *Simulator) ExecuteContinuation(cont ContinuationRecord) (*common.Response, error) {
	newLevel := cont.Level + 1
	if newLevel > MaxContinuationLevel {
		return nil, fmt.Errorf("max continuation level reached: %d > %d", newLevel, MaxContinuationLevel)
	}

	idempotencyKey := fmt.Sprintf("%s:%d", cont.IdempotencyKey, newLevel)

	config := s.getConfig(cont.OID)

	msg := common.Message{
		Version:        protocolVersion,
		IdempotencyKey: idempotencyKey,
		Request: &common.RequestMessage{
			Org:    s.makeOrgAccess(cont.OID, ""),
			Action: cont.Request.Action,
			Data:   cont.Request.State,
			Config: config,
		},
	}

	resp, _, err := s.sendAndParse(&msg)
	if err != nil {
		return nil, err
	}
	s.processResponse(cont.OID, idempotencyKey, newLevel, resp)
	return resp, nil
}

// --- Internal helpers ---

func (s *Simulator) getConfig(oid string) limacharlie.Dict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.configs[oid]; ok {
		return c
	}
	return limacharlie.Dict{}
}

func (s *Simulator) makeOrgAccess(oid string, ident string) common.OrgAccessData {
	if ident == "" {
		ident = "simulator@limacharlie.io"
	}

	s.mu.RLock()
	_, hasMock := s.mockServers[oid]
	s.mu.RUnlock()

	if hasMock {
		return common.OrgAccessData{
			OID:   oid,
			JWT:   "mock-jwt-token",
			Ident: ident,
		}
	}

	return common.OrgAccessData{
		OID:   oid,
		JWT:   "sim-jwt-" + oid,
		Ident: ident,
	}
}

func (s *Simulator) sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.ext.SecretKey))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Simulator) sendRaw(msg *common.Message) ([]byte, int, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal message: %w", err)
	}

	sig := s.sign(payload)

	var body io.Reader
	contentEncoding := ""
	if s.useGzip {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(payload); err != nil {
			w.Close()
			return nil, 0, fmt.Errorf("failed to gzip payload: %w", err)
		}
		if err := w.Close(); err != nil {
			return nil, 0, fmt.Errorf("failed to close gzip writer: %w", err)
		}
		body = &buf
		contentEncoding = "gzip"
	} else {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.server.URL, body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("lc-ext-sig", sig)
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}

	resp, err := s.server.Client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

func (s *Simulator) sendAndParse(msg *common.Message) (*common.Response, int, error) {
	body, statusCode, err := s.sendRaw(msg)
	if err != nil {
		return nil, statusCode, err
	}

	if len(body) == 0 {
		return &common.Response{}, statusCode, nil
	}

	var resp common.Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, statusCode, fmt.Errorf("failed to unmarshal response (status %d, body: %s): %w", statusCode, string(body), err)
	}

	return &resp, statusCode, nil
}

func (s *Simulator) processResponse(oid string, idempotencyKey string, level uint64, resp *common.Response) {
	if resp == nil {
		return
	}

	// Record errors.
	if resp.Error != "" {
		s.mu.Lock()
		s.errors = append(s.errors, ErrorRecord{
			OID:     oid,
			Message: resp.Error,
			Time:    time.Now(),
		})
		s.mu.Unlock()
	}

	// Record metrics.
	if resp.Metrics != nil {
		s.mu.Lock()
		s.metrics = append(s.metrics, MetricRecord{
			OID:            oid,
			IdempotencyKey: resp.Metrics.IdempotentKey,
			Metrics:        resp.Metrics.Metrics,
			Time:           time.Now(),
		})
		s.mu.Unlock()
	}

	// Handle continuations.
	if len(resp.Continuations) > 0 {
		s.handleContinuations(oid, idempotencyKey, level, resp.Continuations)
	}
}

func (s *Simulator) getMaxContinuationsPerResponse() int {
	if s.maxContinuationsPerResponse < 0 {
		return 0 // disabled
	}
	if s.maxContinuationsPerResponse == 0 {
		return DefaultMaxContinuationsPerReq
	}
	return s.maxContinuationsPerResponse
}

func (s *Simulator) handleContinuations(oid string, idempotencyKey string, level uint64, continuations []common.ContinuationRequest) {
	nextLevel := level + 1
	if nextLevel > MaxContinuationLevel {
		s.mu.Lock()
		s.errors = append(s.errors, ErrorRecord{
			OID:     oid,
			Message: fmt.Sprintf("max continuation level reached: %d > %d", nextLevel, MaxContinuationLevel),
			Time:    time.Now(),
		})
		s.mu.Unlock()
		return
	}

	// Enforce fan-out limit per response, matching the backend.
	maxPerResp := s.getMaxContinuationsPerResponse()
	if maxPerResp > 0 && len(continuations) > maxPerResp {
		s.mu.Lock()
		s.errors = append(s.errors, ErrorRecord{
			OID:     oid,
			Message: fmt.Sprintf("continuation count %d exceeds max %d, truncating", len(continuations), maxPerResp),
			Time:    time.Now(),
		})
		s.mu.Unlock()
		continuations = continuations[:maxPerResp]
	}

	for _, cont := range continuations {
		delay := cont.InDelaySeconds
		if delay > MaxContinuationDelay {
			s.mu.Lock()
			s.errors = append(s.errors, ErrorRecord{
				OID:     oid,
				Message: fmt.Sprintf("continuation delay %d exceeds max %d, clamped", delay, MaxContinuationDelay),
				Time:    time.Now(),
			})
			s.mu.Unlock()
		}

		record := ContinuationRecord{
			OID:            oid,
			IdempotencyKey: idempotencyKey,
			Level:          level,
			Request:        cont,
			QueuedAt:       time.Now(),
		}

		s.mu.Lock()
		mode := s.continuationMode
		s.continuations = append(s.continuations, record)
		s.mu.Unlock()

		if mode == ContinuationModeImmediate {
			// Execute synchronously, ignoring delay.
			if _, err := s.ExecuteContinuation(record); err != nil {
				s.mu.Lock()
				s.errors = append(s.errors, ErrorRecord{
					OID:     oid,
					Message: fmt.Sprintf("continuation execution failed: %v", err),
					Time:    time.Now(),
				})
				s.mu.Unlock()
			}
		}
	}
}

func generateIdempotencyKey() string {
	n := atomic.AddUint64(&idempotencyCounter, 1)
	return fmt.Sprintf("sim-%d-%d", time.Now().UnixNano(), n)
}

// --- MockServer integration helpers ---

// NewOrganization creates a [limacharlie.Organization] for the given OID.
// If a [limacharlie.MockServer] was registered for this OID via [WithMockServer]
// or [AddMockServer], the returned organization will use the mock server.
// Otherwise it returns nil with an error.
func (s *Simulator) NewOrganization(oid string) (*limacharlie.Organization, error) {
	s.mu.RLock()
	ms, ok := s.mockServers[oid]
	s.mu.RUnlock()
	if ok {
		return ms.NewOrganization()
	}
	return nil, fmt.Errorf("no MockServer registered for OID %s; use WithMockServer option or AddMockServer", oid)
}

// MockServer returns the MockServer registered for the given OID, or nil if
// none was registered.
func (s *Simulator) MockServer(oid string) *limacharlie.MockServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mockServers[oid]
}
