package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"reflect"
	"sync"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

//revive:disable:var-naming
const PROTOCOL_VERSION = 20221218

const (
	// DefaultMaxBodyBytes is the default cap on the request body as it
	// arrives on the wire (the compressed size when the body is gzipped).
	// The platform's request entry point accepts at most a 10 MiB form
	// field, and a sender compresses what it sends, so 16 MiB leaves room
	// for the envelope (config, event data) around the largest payload.
	DefaultMaxBodyBytes int64 = 16 << 20
	// DefaultMaxDecodedBodyBytes is the default cap on the request body
	// once decompressed. A gzip stream can expand by orders of magnitude, so
	// this is what bounds the memory one request can make the extension
	// allocate. It also applies to a body that is not compressed.
	DefaultMaxDecodedBodyBytes int64 = 64 << 20
)

// errBodyTooLarge reports a request body over one of the configured caps.
var errBodyTooLarge = errors.New("request body too large")

type Extension struct {
	ExtensionName string
	SecretKey     string
	// PreviousSecretKeys are extra shared secrets accepted when verifying
	// the signature of an incoming request, so SecretKey can be rotated
	// without downtime: deploy with the new SecretKey and the old value
	// here, update the secret on the LimaCharlie side, then remove the old
	// value. They are only ever used to verify; everything derived from
	// the shared secret (such as the webhook adapter secrets) uses
	// SecretKey. Empty values are ignored.
	PreviousSecretKeys []string
	Callbacks          ExtensionCallbacks

	ViewsSchema    []common.View
	ConfigSchema   common.SchemaObject
	RequestSchema  common.RequestSchemas
	RequiredEvents []common.EventName

	// OrgFromAccess overrides how an Organization is created from
	// OrgAccessData. If nil, the default NewOrganizationFromClientOptions
	// is used. This is primarily useful for testing with mock servers.
	OrgFromAccess func(common.OrgAccessData) (*limacharlie.Organization, error)

	// MaxBodyBytes caps the request body as received on the wire (before
	// any decompression). Zero means DefaultMaxBodyBytes. Requests over it
	// are refused with 413 before the signature is checked, so keep it as
	// low as the largest request the extension legitimately receives.
	MaxBodyBytes int64
	// MaxDecodedBodyBytes caps the request body after decompression (and a
	// body that is not compressed). Zero means DefaultMaxDecodedBodyBytes.
	MaxDecodedBodyBytes int64

	whClients map[string]*limacharlie.WebhookSender
	mWebhooks sync.RWMutex

	isLogAllErrors bool
}

type ExtensionResponse struct {
	Error error
	Data  limacharlie.Dict
}

type ExtensionCallbacks struct {
	ValidateConfig  func(context.Context, *limacharlie.Organization, limacharlie.Dict) common.Response // Optional
	RequestHandlers map[common.ActionName]RequestCallback                                              // Optional
	EventHandlers   map[common.EventName]EventCallback
	ErrorHandler    func(*common.ErrorReportMessage)
	// FleetRequestHandlers answer a fleet request: one read about several
	// organizations at once. An action here must be declared IsFleet in
	// RequestSchema (and the other way around), and must not also have a
	// RequestHandlers entry. Optional: an Extension with none never receives
	// a fleet request.
	FleetRequestHandlers map[common.ActionName]FleetRequestCallback
}

type RequestCallbackParams struct {
	Org             *limacharlie.Organization
	Ident           string
	Request         interface{}
	Config          limacharlie.Dict
	IdempotentKey   string
	ResourceState   map[string]common.ResourceState
	InvestigationID string
	// ACL describes what the initiator of the request may access with
	// respect to resource ACLs (resources tagged "acl:<scope>"). It is set
	// by the platform and is always usable: when the envelope carried no
	// ACL block, ACL.Present() is false and the view fails closed (see
	// common.ACLView).
	ACL common.ACLView
	// AutomationRule is the name of the D&R rule that issued this request, set by the platform only for an
	// action declared with AllowAutomation. Empty for a request from a person or an API key, whose
	// credential is then Org's (an impersonated action) and whose identity is Ident.
	AutomationRule string
}

type RequestCallback struct {
	RequestStruct interface{}
	Callback      func(ctx context.Context, params RequestCallbackParams) common.Response
}

type EventCallbackParams struct {
	Org           *limacharlie.Organization
	Data          limacharlie.Dict
	Conf          limacharlie.Dict
	IdempotentKey string
}

type EventCallback = func(ctx context.Context, params EventCallbackParams) common.Response

func (e *Extension) Init() error {
	e.whClients = map[string]*limacharlie.WebhookSender{}
	e.isLogAllErrors = os.Getenv("LC_EXTENSION_LOG_ALL_ERRORS") != ""
	return e.validateFleetHandlers()
}

func (e *Extension) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ctx := r.Context()
	signature := r.Header.Get("lc-ext-sig")
	if signature == "" {
		e.respondAndLog(w, http.StatusOK, nil) //nolint:errcheck
		return
	}

	response := common.Response{Version: PROTOCOL_VERSION}

	requestData, status, err := e.readSignedBody(w, r, signature)
	if err != nil {
		if status == http.StatusUnauthorized {
			response.Error = "invalid signature"
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error})
			_ = e.respondAndLog(w, status, nil)
			return
		}
		// A refusal for size is decided before the sender is authenticated, so
		// it is not reported through ErrorHandler: that would let anyone
		// generate reports.
		response.Error = err.Error()
		_ = e.respondAndLog(w, status, &response)
		return
	}

	message := common.Message{}
	if err := json.Unmarshal(requestData, &message); err != nil {
		response.Error = fmt.Sprintf("invalid json body: %v", err)
		e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
		return
	}

	// The Secret-typed values the message carries (by the schemas), taken
	// before any callback runs: a callback's error is scrubbed of them before
	// it is reported or answered.
	hidden := e.messageSecretValues(&message)
	// A callback's panic value can carry them too: it is reported with a fixed
	// text instead of reaching the HTTP server's log.
	defer func() {
		if r := recover(); r != nil {
			if e.Callbacks.ErrorHandler != nil {
				// The report is a fixed text; a panic in the handler itself
				// must not take the panic value elsewhere either.
				func() {
					defer func() { _ = recover() }()
					e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: "panic handling a message (details not logged)"})
				}()
			}
			_ = respond(w, http.StatusInternalServerError, &common.Response{Version: PROTOCOL_VERSION, Error: "internal error in the extension"})
		}
	}()

	// A message that carries a fleet section is never taken for a heartbeat:
	// handleFleetRequest refuses it whole when it carries anything else.
	if message.HeartBeat != nil && message.FleetRequest == nil {
		_ = e.respondScrubbed(w, hidden, http.StatusOK, &common.HeartBeatResponse{})
		return
	}

	if message.FleetRequest != nil {
		// Reached only after the signature above, like every other message.
		fleetResponse, status := e.handleFleetRequest(ctx, &message)
		fleetResponse.Error = scrubSecrets(fleetResponse.Error, hidden)
		if status != 0 {
			_ = e.respondScrubbed(w, hidden, status, &fleetResponse)
			return
		}
		response = fleetResponse
	} else if message.Event != nil {
		org, err := e.generateSDK(message.Event.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: scrubSecrets(response.Error, hidden), Oid: message.Event.Org.OID})
			_ = e.respondScrubbed(w, hidden, http.StatusInternalServerError, &response)
			return
		}
		defer org.Close()

		handler, ok := e.Callbacks.EventHandlers[message.Event.EventName]
		if !ok {
			response.Error = fmt.Sprintf("unknown event: %s", message.Event.EventName)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: scrubSecrets(response.Error, hidden), Oid: message.Event.Org.OID})
			_ = e.respondScrubbed(w, hidden, http.StatusBadRequest, &response)
			return
		}
		response = handler(ctx, EventCallbackParams{
			Org:           org,
			Data:          message.Event.Data,
			Conf:          message.Event.Config,
			IdempotentKey: message.IdempotencyKey,
		})
	} else if message.Request != nil {
		org, err := e.generateSDK(message.Request.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: scrubSecrets(response.Error, hidden), Oid: message.Request.Org.OID})
			_ = e.respondScrubbed(w, hidden, http.StatusInternalServerError, &response)
			return
		}
		defer org.Close()

		rcb, ok := e.Callbacks.RequestHandlers[message.Request.Action]
		if !ok {
			response.Error = fmt.Sprintf("unknown request action: %s", message.Request.Action)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: scrubSecrets(response.Error, hidden), Oid: message.Request.Org.OID})
			_ = e.respondScrubbed(w, hidden, http.StatusBadRequest, &response)
			return
		}
		// If the request struct is nil, we will unmarshal into a dict.
		var tmpData interface{}
		if rcb.RequestStruct == nil || (reflect.ValueOf(tmpData).Kind() == reflect.Ptr && reflect.ValueOf(tmpData).IsNil()) {
			tmpData = message.Request.Data
		} else {
			tmpData, err = unmarshalToStruct(message.Request.Data, rcb.RequestStruct)
		}
		if err != nil {
			response.Error = fmt.Sprintf("failed to unmarshal request data: %v", err)
			_ = e.respondScrubbed(w, hidden, http.StatusBadRequest, &response)
			return
		}
		response = rcb.Callback(ctx, RequestCallbackParams{
			Org:             org,
			Ident:           message.Request.Org.Ident,
			Request:         tmpData,
			Config:          message.Request.Config,
			IdempotentKey:   message.IdempotencyKey,
			ResourceState:   message.Request.ResourceState,
			InvestigationID: message.Request.InvestigationID,
			ACL:             common.NewACLView(message.Request.ACL),
			AutomationRule:  message.Request.AutomationRule,
		})
	} else if message.ErrorReport != nil {
		e.Callbacks.ErrorHandler(message.ErrorReport)
	} else if message.ConfigValidation != nil {
		org, err := e.generateSDK(message.ConfigValidation.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: scrubSecrets(response.Error, hidden), Oid: message.ConfigValidation.Org.OID})
			_ = e.respondScrubbed(w, hidden, http.StatusInternalServerError, &response)
			return
		}
		defer org.Close()

		if e.Callbacks.ValidateConfig != nil {
			response = e.Callbacks.ValidateConfig(ctx, org, message.ConfigValidation.Config)
		}
	} else if message.SchemaRequest != nil {

		eventHandlers := make([]common.EventName, 0)
		for handler := range e.Callbacks.EventHandlers {
			eventHandlers = append(eventHandlers, handler)
		}

		response.Data = &common.SchemaRequestResponse{
			Views:          e.ViewsSchema,
			Config:         e.ConfigSchema,
			Request:        e.RequestSchema,
			RequiredEvents: eventHandlers,
		}
	} else {
		// Keep request content out of both the response and the error callback.
		response.Error = fmt.Sprintf("no recognized section in request (%d bytes)", len(requestData))
		_ = e.respondScrubbed(w, hidden, http.StatusBadRequest, &response)
		return
	}

	response.Error = scrubSecrets(response.Error, hidden)
	if response.Error != "" {
		// TODO: In the future we should support more detailed error handling and error types such as
		// validation error, etc. and return appropriate status code (e.g. 400 for validation error, etc.)
		// For the time being we return 503 for retryable errors and 500 for non-retryable errors.
		if response.IsRetriable() {
			_ = e.respondScrubbed(w, hidden, http.StatusInternalServerError, &response)
			return
		}

		_ = e.respondScrubbed(w, hidden, http.StatusBadRequest, &response)
		return
	}
	response.Version = PROTOCOL_VERSION
	_ = e.respondScrubbed(w, hidden, http.StatusOK, &response)
}

// respondScrubbed is respondAndLog for a message whose Secret values are
// known: every error answered or reported for it is scrubbed of them first,
// whatever produced it (a callback, a request decoder, the SDK itself).
func (e *Extension) respondScrubbed(w http.ResponseWriter, hidden []string, status int, data interface{}) error {
	if r, ok := data.(*common.Response); ok && r.Error != "" {
		r.Error = scrubSecrets(r.Error, hidden)
	}
	return e.respondAndLog(w, status, data)
}

func (e *Extension) respondAndLog(w http.ResponseWriter, status int, data interface{}) error {
	if r, ok := data.(*common.Response); e.isLogAllErrors && ok {
		if r.Error != "" {
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: r.Error})
		}
	}
	if err := respond(w, status, data); err != nil {
		// A marshalling error can quote the value it failed on.
		e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: "failed to respond (details not logged)"})
		return err
	}
	return nil
}

func (e *Extension) maxBodyBytes() int64 {
	if e.MaxBodyBytes > 0 {
		return e.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

func (e *Extension) maxDecodedBodyBytes() int64 {
	if e.MaxDecodedBodyBytes > 0 {
		return e.MaxDecodedBodyBytes
	}
	return DefaultMaxDecodedBodyBytes
}

// readSignedBody reads the request body and returns it decoded, only once its
// signature is verified. It is the first thing that touches the body, and the
// body is not trusted until then, so what it holds in memory is bounded by the
// caps before authentication rather than by what the sender chose to send:
//   - the wire body is capped (http.MaxBytesReader);
//   - a gzip body is first decompressed straight into the signature check,
//     which keeps no output and so costs no memory however far it expands, and
//     is itself capped; the decoded body is only materialised, a second time,
//     once the signature matched.
//
// On failure the returned status is the HTTP status to answer with.
func (e *Extension) readSignedBody(w http.ResponseWriter, r *http.Request, signature string) ([]byte, int, error) {
	isGzip := r.Header.Get("Content-Encoding") == "gzip"
	wireLimit := e.maxBodyBytes()
	decodedLimit := e.maxDecodedBodyBytes()
	if !isGzip {
		// What is on the wire is the decoded body.
		wireLimit = decodedLimit
	}
	if r.ContentLength > wireLimit {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: limit is %d bytes", errBodyTooLarge, wireLimit)
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, wireLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: limit is %d bytes", errBodyTooLarge, wireLimit)
		}
		return nil, http.StatusNoContent, fmt.Errorf("failed reading body: %v", err)
	}

	keys := e.verificationKeys()

	if !isGzip {
		for _, k := range keys {
			if verifyOrigin(raw, signature, k) {
				return raw, http.StatusOK, nil
			}
		}
		return nil, http.StatusUnauthorized, errors.New("invalid signature")
	}

	// Pass 1: verify the signature over the decompressed stream without keeping it.
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	macs := make([]hash.Hash, len(keys))
	macWriters := make([]io.Writer, len(keys))
	for i, k := range keys {
		macs[i] = hmac.New(sha256.New, k)
		macWriters[i] = macs[i]
	}
	n, err := io.Copy(io.MultiWriter(macWriters...), io.LimitReader(zr, decodedLimit+1))
	_ = zr.Close()
	if n > decodedLimit {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: limit is %d bytes once decompressed", errBodyTooLarge, decodedLimit)
	}
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed decompressing body: %v", err)
	}
	isVerified := false
	for _, mac := range macs {
		if hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
			isVerified = true
			break
		}
	}
	if !isVerified {
		return nil, http.StatusUnauthorized, errors.New("invalid signature")
	}

	// Pass 2: the sender is authenticated and the decoded size is known to be within the cap.
	zr, err = gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	defer func() { _ = zr.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(zr, decodedLimit+1))
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed decompressing body: %v", err)
	}
	return decoded, http.StatusOK, nil
}

// verificationKeys returns the shared secrets an incoming request may be
// signed with: SecretKey first, then any non-empty PreviousSecretKeys.
func (e *Extension) verificationKeys() [][]byte {
	keys := [][]byte{[]byte(e.SecretKey)}
	for _, k := range e.PreviousSecretKeys {
		if k == "" || k == e.SecretKey {
			continue
		}
		keys = append(keys, []byte(k))
	}
	return keys
}

func verifyOrigin(data []byte, sig string, secretKey []byte) bool {
	mac := hmac.New(sha256.New, secretKey)
	if _, err := mac.Write(data); err != nil {
		return false
	}
	jsonCompatSig := []byte(hex.EncodeToString(mac.Sum(nil)))
	return hmac.Equal(jsonCompatSig, []byte(sig))
}

func respond(w http.ResponseWriter, status int, data interface{}) error {
	w.WriteHeader(status)
	if data == nil {
		return nil
	}
	j := json.NewEncoder(w)
	if err := j.Encode(data); err != nil {
		return fmt.Errorf("failed to encode response: %v", err)
	}
	return nil
}

func (e *Extension) generateSDK(oad common.OrgAccessData) (*limacharlie.Organization, error) {
	if e.OrgFromAccess != nil {
		return e.OrgFromAccess(oad)
	}
	// LC_API_URL / LC_JWT_URL override the production endpoints so an
	// extension can run against a local or test stack; empty means default.
	return limacharlie.NewOrganizationFromClientOptions(limacharlie.ClientOptions{
		OID:    oad.OID,
		JWT:    oad.JWT,
		URL:    os.Getenv("LC_API_URL"),
		JWTURL: os.Getenv("LC_JWT_URL"),
	}, nil)
}

func unmarshalToStruct(d limacharlie.Dict, s interface{}) (interface{}, error) {
	if s == nil {
		return nil, fmt.Errorf("invalid request missing request struct definition")
	}

	// Create a new instance of the struct needed using reflection.
	inCopyValue := reflect.ValueOf(s).Elem()
	inCopy := reflect.New(inCopyValue.Type())
	inCopy.Elem().Set(inCopyValue)
	out := inCopy.Interface()

	if err := d.UnMarshalToStruct(out); err != nil {
		return nil, err
	}
	return out, nil
}

// ToRequestMessage rebuilds the request envelope that produced these params so
// that an intermediary (such as the multiplexer) can forward the request to
// another extension. The org access data is provided by the caller since the
// forwarded credentials may differ from the received ones. Every platform-set
// field, including the ACL block (or its absence), is carried over unchanged.
func (p RequestCallbackParams) ToRequestMessage(action common.ActionName, org common.OrgAccessData) *common.RequestMessage {
	data, _ := p.Request.(limacharlie.Dict)
	return &common.RequestMessage{
		Org:             org,
		Action:          action,
		Data:            data,
		Config:          p.Config,
		ResourceState:   p.ResourceState,
		InvestigationID: p.InvestigationID,
		ACL:             p.ACL.Envelope(),
		AutomationRule:  p.AutomationRule,
	}
}

func (e *Extension) GetExtensionPrivateTag() string {
	return fmt.Sprintf("ext:%s", e.ExtensionName)
}
