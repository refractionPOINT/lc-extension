package ext

import (
	"context"
	"fmt"
	"net/http"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

// Store the runtime context for the Extension, extending the `core.Extension` and logging.
type BasicExtension struct {
	core.Extension
	limacharlie.LCLoggerGCP
}

// The singleton reference to this Extension running.
var Extension *BasicExtension

// Boilerplate Code
// Serves the extension as a Cloud Function.
// ============================================================================
func init() {
	Extension = &BasicExtension{
		core.Extension{
			ExtensionName: "basic-extension",
			SecretKey:     "1234", // Shared secret with LimaCharlie.
			// The schema defining what the configuration for this Extension should look like.
			ConfigSchema: common.ConfigObjectSchema{
				Fields:       map[common.ConfigKey]common.ConfigElement{},
				Requirements: [][]common.ConfigKey{},
			},
			// The schema defining what requests to this Extension should look like.
			RequestSchema: map[string]common.RequestSchema{
				"ping": { // An Action called "ping".
					IsUserFacing:     true, // This action should be visible to users for interaction.
					ShortDescription: "simple ping request",
					LongDescription:  "will echo back some value",
					IsImpersonated:   false, // This action will operate as the Extension itself, NOT impersonating the user calling the action.
					ParameterDefinitions: common.RequestParameterDefinitions{
						Parameters: map[common.RequestParameterName]common.RequestParameterDefinition{
							"some_value": {
								IsList:       false, // This is a single string, not a list of strings.
								DataType:     common.ParameterDataTypes.String,
								DisplayIndex: 0, // Display this parameter as the first one (index 0).
							},
						},
						Requirements: [][]common.RequestParameterName{
							{"some_value"}, // The "some_value" parameter is required and has no alternative parameters.
						},
					},
				},
			},
		},
		limacharlie.LCLoggerGCP{},
	}
	// Callbacks receiving webhooks from LimaCharlie.
	Extension.Callbacks = core.ExtensionCallbacks{
		// When a user changes a config for this Extension, you will be asked to validate it.
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config map[string]interface{}) common.Response {
			Extension.Info(fmt.Sprintf("validate config from %s", org.GetOID()))
			return common.Response{} // No error, so success.
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"ping": { // Handle the "ping" Action.
				RequestStruct: &PingRequest{}, // This is the structure to validate for the parameters received.
				Callback:      Extension.OnPing,
			},
		},
		// Events occuring in LimaCharlie that we need to be made aware of.
		EventHandlers: map[common.EventName]core.EventCallback{
			// An Org subscribed.
			common.EventTypes.Subscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf map[string]interface{}, idempotentKey string) common.Response {
				Extension.Info(fmt.Sprintf("subscribe to %s", org.GetOID()))
				return common.Response{}
			},
			// An Org unsubscribed.
			common.EventTypes.Unsubscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf map[string]interface{}, idempotentKey string) common.Response {
				Extension.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))
				return common.Response{}
			},
		},
	}
	// Start processing.
	if err := Extension.Init(); err != nil {
		panic(err)
	}
}

// This example defines a simple http handler that can now be used
// as an entry point to a Cloud Function. See /server/webserver for a
// useful helper to run the handler as a webserver in a container.
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

func (e *BasicExtension) OnPing(ctx context.Context, org *limacharlie.Organization, data interface{}, conf map[string]interface{}, idempotentKey string) common.Response {
	request := data.(*PingRequest)

	return common.Response{
		Data: request,
	}
}
