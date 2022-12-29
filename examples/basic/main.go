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
			ConfigSchema: common.ConfigObjectSchema{
				Fields:       map[common.ConfigKey]common.ConfigElement{},
				Requirements: [][]common.ConfigKey{},
			},
			RequestSchema: map[string]common.RequestSchema{
				"ping": {
					IsUserFacing:     true,
					ShortDescription: "simple ping request",
					LongDescription:  "will echo back some value",
					IsImpersonated:   false,
					ParameterDefinitions: common.RequestParameterDefinitions{
						Parameters: map[common.RequestParameterName]common.RequestParameterDefinition{
							"some_value": {
								IsRequired:   true,
								IsList:       false,
								DataType:     common.ParameterDataTypes.String,
								DisplayIndex: 0,
							},
						},
						Requirements: [][]common.RequestParameterName{
							{"some_value"},
						},
					},
				},
			},
		},
	}
	Extension.Callbacks = core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config map[string]interface{}) common.Response {
			Extension.LCLoggerZerolog.Info(fmt.Sprintf("validate config from %s", org.GetOID()))
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"ping": {
				RequestStruct: &PingRequest{},
				Callback:      Extension.OnPing,
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf map[string]interface{}) common.Response {
				Extension.LCLoggerZerolog.Info(fmt.Sprintf("subscribe to %s", org.GetOID()))
				return common.Response{}
			},
			common.EventTypes.Unsubscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf map[string]interface{}) common.Response {
				Extension.LCLoggerZerolog.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))
				return common.Response{}
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

func (e *BasicExtension) OnPing(ctx context.Context, org *limacharlie.Organization, data interface{}, conf map[string]interface{}) common.Response {
	request := data.(*PingRequest)

	return common.Response{
		Data: request,
	}
}
