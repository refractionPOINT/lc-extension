package core

import (
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

const PROTOCOL_VERSION = 20221218

type Extension struct {
	ExtensionName string
	SecretKey     string
	Callbacks     ExtensionCallbacks

	ViewsSchema    []common.View
	ConfigSchema   common.SchemaObject
	RequestSchema  common.RequestSchemas
	RequiredEvents []common.EventName

	whClients map[string]*limacharlie.WebhookSender
	mWebhooks sync.RWMutex
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

type RequestCallback struct {
	RequestStruct interface{}
	Callback      func(ctx context.Context, org *limacharlie.Organization, req interface{}, conf limacharlie.Dict, idempotentKey string) common.Response
}

type EventCallback = func(ctx context.Context, org *limacharlie.Organization, data limacharlie.Dict, conf limacharlie.Dict, idempotentKey string) common.Response

func (e *Extension) Init() error {
	e.whClients = map[string]*limacharlie.WebhookSender{}
	return nil
}

func (e *Extension) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ctx := r.Context()
	signature := r.Header.Get("lc-ext-sig")
	if signature == "" {
		respond(w, http.StatusOK, nil)
		return
	}

	response := common.Response{Version: PROTOCOL_VERSION}

	var body io.ReadCloser
	var err error
	body = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		if body, err = gzip.NewReader(r.Body); err != nil {
			response.Error = err.Error()
			respond(w, http.StatusBadRequest, &response)
			return
		}
		defer body.Close()
	}

	requestData, err := io.ReadAll(body)
	if err != nil {
		response.Error = fmt.Sprintf("failed reading body: %v", err)
		respond(w, http.StatusNoContent, &response)
		return
	}

	if !verifyOrigin(requestData, signature, []byte(e.SecretKey)) {
		response.Error = "invalid signature"
		e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error})
		respond(w, http.StatusUnauthorized, nil)
		return
	}

	message := common.Message{}
	if err := json.Unmarshal(requestData, &message); err != nil {
		response.Error = fmt.Sprintf("invalid json body: %v", err)
		respond(w, http.StatusBadRequest, &response)
		return
	}

	if message.HeartBeat != nil {
		respond(w, http.StatusOK, &common.HeartBeatResponse{})
		return
	}

	if message.Event != nil {
		org, err := e.generateSDK(message.Event.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Event.Org.OID})
			respond(w, http.StatusInternalServerError, &response)
			return
		}

		handler, ok := e.Callbacks.EventHandlers[message.Event.EventName]
		if !ok {
			response.Error = fmt.Sprintf("unknown event: %s", message.Event.EventName)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Event.Org.OID})
			respond(w, http.StatusBadRequest, &response)
			return
		}
		response = handler(ctx, org, message.Event.Data, message.Event.Config, message.IdempotencyKey)
	} else if message.Request != nil {
		org, err := e.generateSDK(message.Request.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			respond(w, http.StatusInternalServerError, &response)
			return
		}

		rcb, ok := e.Callbacks.RequestHandlers[message.Request.Action]
		if !ok {
			response.Error = fmt.Sprintf("unknown request action: %s", message.Request.Action)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			respond(w, http.StatusBadRequest, &response)
			return
		}
		tmpData, err := unmarshalToStruct(message.Request.Data, rcb.RequestStruct)
		if err != nil {
			response.Error = fmt.Sprintf("failed to unmarshal request data: %v", err)
			respond(w, http.StatusBadRequest, &response)
			return
		}
		response = rcb.Callback(ctx, org, tmpData, message.Request.Config, message.IdempotencyKey)
	} else if message.ErrorReport != nil {
		e.Callbacks.ErrorHandler(message.ErrorReport)
	} else if message.ConfigValidation != nil {
		org, err := e.generateSDK(message.ConfigValidation.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error, Oid: message.Request.Org.OID})
			respond(w, http.StatusInternalServerError, &response)
			return
		}
		if e.Callbacks.ValidateConfig != nil {
			response = e.Callbacks.ValidateConfig(ctx, org, message.ConfigValidation.Config)
		}
	} else if message.SchemaRequest != nil {
		response.Data = &common.SchemaRequestResponse{
			Views:          e.ViewsSchema,
			Config:         e.ConfigSchema,
			Request:        e.RequestSchema,
			RequiredEvents: e.RequiredEvents,
		}
	} else {
		response.Error = fmt.Sprintf("no data in request: %s", requestData)
		respond(w, http.StatusBadRequest, &response)
		return
	}

	if response.Error != "" {
		respond(w, http.StatusInternalServerError, &response)
		return
	}
	response.Version = PROTOCOL_VERSION
	respond(w, http.StatusOK, &response)
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
		panic(fmt.Sprintf("failed to encode response: %v", err))
	}
	return nil
}

func (e *Extension) generateSDK(oad common.OrgAccessData) (*limacharlie.Organization, error) {
	return limacharlie.NewOrganizationFromClientOptions(limacharlie.ClientOptions{
		OID: oad.OID,
		JWT: oad.JWT,
	}, nil)
}

func unmarshalToStruct(d limacharlie.Dict, s interface{}) (interface{}, error) {
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

func (e *Extension) GetExtensionPrivateTag() string {
	return fmt.Sprintf("ext:%s", e.ExtensionName)
}
