package ext

import (
	"context"
	"fmt"
	"net/http"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

type BasicExtension struct {
	core.Extension
}

var Extension *BasicExtension

// Boilerplate Code
// Serves the extension as a Cloud Function.
// ============================================================================
func init() {
	Extension = &BasicExtension{
		core.Extension{
			ExtensionName: "basic-extension",
			SecretKey:     "1234",
		},
	}
	Extension.Callbacks = core.ExtensionCallbacks{
		OnSubscribe: func(ctx context.Context, o *limacharlie.Organization) common.Response {
			Extension.LCLoggerZerolog.Info(fmt.Sprintf("subscribe to %s", o.GetOID()))
			return common.Response{}
		},
		OnUnsubscribe: func(ctx context.Context, o *limacharlie.Organization) common.Response {
			Extension.LCLoggerZerolog.Info(fmt.Sprintf("unsubscribe from %s", o.GetOID()))
			return common.Response{}
		},
		RequestHandlers: map[string]core.RequestCallback{
			"ping": {
				RequestStruct: &PingRequest{},
				Callback:      Extension.OnPing,
			},
		},
	}
	if err := Extension.Init(); err != nil {
		panic(err)
	}
}

func Process(w http.ResponseWriter, r *http.Request) {
	Extension.ServeHTTP(w, r)
}

// Actual Extension Implementation
// ============================================================================
type PingRequest struct {
	SomeValue string `json:"some_value"`
}

func (e *BasicExtension) Init() error {
	// Initialize the Extension core.
	if err := e.Extension.Init(); err != nil {
		return err
	}

	return nil
}

func (e *BasicExtension) OnPing(ctx context.Context, o *limacharlie.Organization, d interface{}) common.Response {
	request := d.(*PingRequest)

	return common.Response{
		Data: request,
	}
}
