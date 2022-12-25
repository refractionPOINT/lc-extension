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

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

const PROTOCOL_VERSION = 20221218

type Extension struct {
	limacharlie.LCLoggerZerolog
	ExtensionName string
	SecretKey     string
	Callbacks     ExtensionCallbacks

	ConfigSchema  common.ConfigObjectSchema
	RequestSchema common.RequestSchemas
}

type ExtensionResponse struct {
	Error error
	Data  Dict
}

type ExtensionCallbacks struct {
	OnSubscribe     func(context.Context, *limacharlie.Organization) common.Response
	OnUnsubscribe   func(context.Context, *limacharlie.Organization) common.Response
	RequestHandlers map[common.ActionName]RequestCallback
}

type RequestCallback struct {
	RequestStruct interface{}
	Callback      func(context.Context, *limacharlie.Organization, interface{}) common.Response
}

func (e *Extension) Init() error {
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
			respond(w, http.StatusInternalServerError, &response)
			return
		}

		switch message.Event.EventName {
		case common.EventTypes.Subscribe:
			response = e.Callbacks.OnSubscribe(ctx, org)
		case common.EventTypes.Unsubscribe:
			response = e.Callbacks.OnUnsubscribe(ctx, org)
		default:
			response.Error = fmt.Sprintf("unknown event: %s", message.Event.EventName)
			respond(w, http.StatusBadRequest, &response)
			return
		}
	} else if message.Request != nil {
		org, err := e.generateSDK(message.Request.Org)
		if err != nil {
			response.Error = fmt.Sprintf("failed initializing sdk: %v", err)
			respond(w, http.StatusInternalServerError, &response)
			return
		}

		rcb, ok := e.Callbacks.RequestHandlers[message.Request.Action]
		if !ok {
			response.Error = fmt.Sprintf("unknown request action: %s", message.Request.Action)
			respond(w, http.StatusBadRequest, &response)
			return
		}
		tmpData, err := unmarshalToStruct(message.Request.Data, rcb.RequestStruct)
		if err != nil {
			response.Error = fmt.Sprintf("failed to unmarshal request data: %v", err)
			respond(w, http.StatusBadRequest, &response)
			return
		}
		response = rcb.Callback(ctx, org, tmpData)
	} else {
		response.Error = "no data in request"
		respond(w, http.StatusBadRequest, &response)
		return
	}

	if response.Error != "" {
		respond(w, http.StatusInternalServerError, &response)
		return
	}
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

func respond(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	if data == nil {
		return
	}
	j := json.NewEncoder(w)
	j.Encode(data)
}

func (e *Extension) generateSDK(oad common.OrgAccessData) (*limacharlie.Organization, error) {
	return limacharlie.NewOrganizationFromClientOptions(limacharlie.ClientOptions{
		OID: oad.OID,
		JWT: oad.JWT,
	}, e)
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
