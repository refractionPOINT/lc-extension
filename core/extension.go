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
	Callbacks     ExtensionCallbacks

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
	return nil
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

	if message.HeartBeat != nil {
		e.respondAndLog(w, http.StatusOK, &common.HeartBeatResponse{}) //nolint:errcheck
		return
	}

	if message.Event != nil {
		org, err := e.generateSDK(message.Event.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Event.Org.OID})
			e.respondAndLog(w, http.StatusInternalServerError, &response) //nolint:errcheck
			return
		}
		defer org.Close()

		handler, ok := e.Callbacks.EventHandlers[message.Event.EventName]
		if !ok {
			response.Error = fmt.Sprintf("unknown event: %s", message.Event.EventName)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Event.Org.OID})
			e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
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
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			e.respondAndLog(w, http.StatusInternalServerError, &response) //nolint:errcheck
			return
		}
		defer org.Close()

		rcb, ok := e.Callbacks.RequestHandlers[message.Request.Action]
		if !ok {
			response.Error = fmt.Sprintf("unknown request action: %s", message.Request.Action)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
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
			e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
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
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			e.respondAndLog(w, http.StatusInternalServerError, &response) //nolint:errcheck
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
		response.Error = fmt.Sprintf("no data in request: %s", requestData)
		e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
		return
	}

	if response.Error != "" {
		// TODO: In the future we should support more detailed error handling and error types such as
		// validation error, etc. and return appropriate status code (e.g. 400 for validation error, etc.)
		// For the time being we return 503 for retryable errors and 500 for non-retryable errors.
		if response.IsRetriable() {
			e.respondAndLog(w, http.StatusInternalServerError, &response) //nolint:errcheck
			return
		}

		e.respondAndLog(w, http.StatusBadRequest, &response) //nolint:errcheck
		return
	}
	response.Version = PROTOCOL_VERSION
	e.respondAndLog(w, http.StatusOK, &response) //nolint:errcheck
}

func (e *Extension) respondAndLog(w http.ResponseWriter, status int, data interface{}) error {
	if r, ok := data.(*common.Response); e.isLogAllErrors && ok {
		if r.Error != "" {
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: r.Error})
		}
	}
	if err := respond(w, status, data); err != nil {
		e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: fmt.Sprintf("failed to respond: %v", err)})
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

	if !isGzip {
		if !verifyOrigin(raw, signature, []byte(e.SecretKey)) {
			return nil, http.StatusUnauthorized, errors.New("invalid signature")
		}
		return raw, http.StatusOK, nil
	}

	// Pass 1: verify the signature over the decompressed stream without keeping it.
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	mac := hmac.New(sha256.New, []byte(e.SecretKey))
	n, err := io.Copy(mac, io.LimitReader(zr, decodedLimit+1))
	_ = zr.Close()
	if n > decodedLimit {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: limit is %d bytes once decompressed", errBodyTooLarge, decodedLimit)
	}
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed decompressing body: %v", err)
	}
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
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
