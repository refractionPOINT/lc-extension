package lambda

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
	"github.com/refractionPOINT/lc-extension/server/webserver"
)

type LambdaCallback = func(ctx context.Context, org *limacharlie.Organization, req limacharlie.Dict, idempotentKey string) (limacharlie.Dict, error)

type LambdaExtension struct {
	Name      string
	SecretKey string
	Logger    limacharlie.LCLogger

	Function LambdaCallback

	tag      string
	ruleName string
}

type lambdaRequest struct {
	Input limacharlie.Dict `json:"input"`
}

var extension *core.Extension

func (l *LambdaExtension) Init() (*core.Extension, error) {
	l.tag = fmt.Sprintf("ext:%s", l.Name)
	x := &core.Extension{
		ExtensionName: l.Name,
		SecretKey:     l.SecretKey,
		// The schema defining what the configuration for this Extension should look like.
		ConfigSchema: common.SchemaObject{
			Fields:       map[common.SchemaKey]common.SchemaElement{},
			Requirements: [][]common.SchemaKey{},
		},
		// The schema defining what requests to this Extension should look like.
		RequestSchema: map[string]common.RequestSchema{
			"trigger": {
				IsUserFacing:     false,
				ShortDescription: "entry point to the lambda",
				IsImpersonated:   false,
				ParameterDefinitions: common.SchemaObject{
					Fields: map[common.SchemaKey]common.SchemaElement{
						"input": {
							DataType:    common.SchemaDataTypes.Object,
							Description: "the input to the lambda",
						},
					},
				},
				ResponseDefinition: &common.SchemaObject{
					Fields: map[common.SchemaKey]common.SchemaElement{
						"result": {
							DataType:    common.SchemaDataTypes.Object,
							Description: "the result of the lambda",
						},
					},
				},
			},
		},
	}

	x.Callbacks = core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"trigger": {
				RequestStruct: &lambdaRequest{},
				Callback: func(ctx context.Context, org *limacharlie.Organization, req interface{}, conf limacharlie.Dict, idempotentKey string, resourceState map[string]common.ResourceState) common.Response {
					data, err := l.Function(ctx, org, req.(*lambdaRequest).Input, idempotentKey)
					return common.Response{
						Data:  limacharlie.Dict{"result": data},
						Error: err.Error(),
					}
				},
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf limacharlie.Dict, idempotentKey string) common.Response {
				l.Logger.Info(fmt.Sprintf("subscribe to %s", org.GetOID()))
				return common.Response{}
			},
			// An Org unsubscribed.
			common.EventTypes.Unsubscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf limacharlie.Dict, idempotentKey string) common.Response {
				l.Logger.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))
				return common.Response{}
			},
		},
		ErrorHandler: func(errMsg *common.ErrorReportMessage) {
			l.Logger.Error(fmt.Sprintf("error from limacharlie: %s", errMsg.Error))
		},
	}
	// Start processing.
	if err := x.Init(); err != nil {
		panic(err)
	}

	return x, nil
}

func StartLambda(f LambdaCallback) {
	lambdaName := os.Getenv("LC_LAMBDA_NAME")
	lambdaSecret := os.Getenv("LC_SHARED_SECRET")
	ext := &LambdaExtension{
		Name:      lambdaName,
		SecretKey: lambdaSecret,
		Logger:    &limacharlie.LCLoggerGCP{},
		Function:  f,
	}

	var err error
	if extension, err = ext.Init(); err != nil {
		panic(err)
	}
	webserver.RunExtension(extension)
}

func (l *LambdaExtension) process(w http.ResponseWriter, r *http.Request) {
	extension.ServeHTTP(w, r)
}
