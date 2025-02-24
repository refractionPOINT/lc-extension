package simplified

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"

	"github.com/refractionPOINT/shlex"
)

type CLIHandler = func(cliTokens []string, credentials string) (CLIReturnData, error)

type CLIDescriptor struct {
	ProcessCommand    CLIHandler
	CredentialsFormat string
	ExampleCommand    string
}

type CLIReturnData struct {
	StatusCode   int                `json:"status_code"`
	OutputString string             `json:"output_string"`
	OutputDict   limacharlie.Dict   `json:"output_dict"`
	OutputList   []limacharlie.Dict `json:"output_list"`
}

type CLIName = string

type CLIExtension struct {
	Name      string
	SecretKey string
	Logger    limacharlie.LCLogger

	Descriptors map[CLIName]CLIDescriptor

	extension *core.Extension
}

type CLIRunRequest struct {
	CommandLine   string   `json:"command_line"`
	CommandTokens []string `json:"command_tokens"`
	Credentials   string   `json:"credentials"`
	Tool          string   `json:"tool"`
}

var errUnknownTool = errors.New("unknown tool")

func (l *CLIExtension) Init() (*core.Extension, error) {
	isSingleTool := len(l.Descriptors) == 1

	requiredFields := [][]common.SchemaKey{{"command_tokens", "command_line"}, {"credentials"}}
	if !isSingleTool {
		requiredFields = append(requiredFields, []common.SchemaKey{"tool"})
	}
	toolList := []interface{}{}
	for k := range l.Descriptors {
		toolList = append(toolList, k)
	}
	toolField := common.SchemaElement{
		DataType:     common.SchemaDataTypes.Enum,
		Label:        "Tool",
		Description:  "The tool provider to use.",
		EnumValues:   toolList,
		DisplayIndex: 2,
	}
	longDesc := "Run a CLI command by choosing a CLI tool, a set of credentials to authenticate with, and a list of command line parameters to provide to the CLI tool."
	if isSingleTool {
		longDesc = fmt.Sprintf("Run a CLI command using the %s tool by providing a list of command line parameters to provide to it.", toolList[0])
	}
	x := &core.Extension{
		ExtensionName: l.Name,
		SecretKey:     l.SecretKey,
		// The schema defining what the configuration for this Extension should look like.
		ConfigSchema: common.SchemaObject{},
		// The schema defining what requests to this Extension should look like.
		RequiredEvents: []common.EventName{common.EventTypes.Subscribe, common.EventTypes.Unsubscribe},
		ViewsSchema: []common.View{
			{
				Name:            "",
				LayoutType:      "action",
				DefaultRequests: []string{"run"},
			},
		},
		RequestSchema: map[string]common.RequestSchema{
			"run": {
				IsUserFacing:     true,
				Label:            "Run a CLI command",
				ShortDescription: "Run a CLI command for a supported tool.",
				LongDescription:  longDesc,
				ParameterDefinitions: common.SchemaObject{
					Requirements: requiredFields,
					Fields: map[common.SchemaKey]common.SchemaElement{
						"command_line": common.SchemaElement{
							DataType:     common.SchemaDataTypes.String,
							Label:        "Command Line",
							Description:  "The command to run.",
							IsList:       false,
							DisplayIndex: 3,
						},
						"command_tokens": common.SchemaElement{
							DataType:     common.SchemaDataTypes.String,
							Label:        "Command Parameters",
							Description:  "The command parameters to run as a tokenized list.",
							IsList:       true,
							DisplayIndex: 4,
						},
						"credentials": common.SchemaElement{
							DataType:     common.SchemaDataTypes.Secret,
							Label:        "Credentials",
							Description:  `The credentials to use for the command. A GCP JSON key, a DigitalOcean Access Token or an AWS "accessKeyID/secretAccessKey" pair.`,
							DisplayIndex: 1,
						},
					},
				},
				ResponseDefinition: &common.SchemaObject{
					Fields: map[common.SchemaKey]common.SchemaElement{
						"output_list": common.SchemaElement{
							DataType:    common.SchemaDataTypes.Object,
							Label:       "Outputs",
							Description: "The output JSON objects of the command.",
							IsList:      true,
						},
						"output_dict": common.SchemaElement{
							DataType:    common.SchemaDataTypes.Object,
							Label:       "Output",
							Description: "The output JSON object of the command.",
							IsList:      false,
						},
						"output_string": common.SchemaElement{
							DataType:    common.SchemaDataTypes.String,
							Label:       "Raw Output",
							Description: "The non-JSON output of the command.",
						},
						"status_code": common.SchemaElement{
							DataType:    common.SchemaDataTypes.Integer,
							Label:       "Status Code",
							Description: "The status of the command.",
						},
					},
				},
			},
		},
	}

	if !isSingleTool {
		x.RequestSchema["run"].ParameterDefinitions.Fields["tool"] = toolField
	}

	x.Callbacks = core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			l.Logger.Info(fmt.Sprintf("validate config from %s", org.GetOID()))
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"run": {
				RequestStruct: &CLIRunRequest{},
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return l.doRun(params.Org, params.Request.(*CLIRunRequest), params.Ident, params.InvestigationID)
				},
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				l.Logger.Info(fmt.Sprintf("subscribe to %s", params.Org.GetOID()))
				if err := l.installRulesIfNeeded(params.Org); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to install rules: %v", err))
					return common.Response{
						Error: err.Error(),
					}
				}
				return common.Response{}
			},
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				l.Logger.Info(fmt.Sprintf("unsubscribe from %s", params.Org.GetOID()))
				if err := l.uninstallAllRules(params.Org); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to uninstall rules: %v", err))
					return common.Response{
						Error: err.Error(),
					}
				}
				return common.Response{}
			},
		},
		ErrorHandler: func(erm *common.ErrorReportMessage) {
			l.Logger.Error(fmt.Sprintf("received error from LC for %s: %s", erm.Oid, erm.Error))
		},
	}

	l.extension = x

	// Start processing.
	if err := x.Init(); err != nil {
		panic(err)
	}

	return x, nil
}

func (e *CLIExtension) installRulesIfNeeded(o *limacharlie.Organization) error {
	if err := e.extension.CreateExtensionAdapter(o, limacharlie.Dict{
		"event_type_path":       "action",
		"investigation_id_path": "inv_id",
	}); err != nil {
		e.Logger.Error(fmt.Sprintf("failed to create extension adapter: %v", err))
		return err
	}
	return nil
}

func (e *CLIExtension) uninstallAllRules(o *limacharlie.Organization) error {
	if err := e.extension.DeleteExtensionAdapter(o); err != nil {
		e.Logger.Error(fmt.Sprintf("failed to delete extension adapter: %v", err))
		return err
	}
	return nil
}

func (e *CLIExtension) doRun(o *limacharlie.Organization, request *CLIRunRequest, ident string, invID string) common.Response {
	// We're paranoid about this extension as in some cases we
	// drop creds on disk in temp files and the CLIs are not
	// isolated in sub-containers (yet).
	// The service is set to let one request at a time and we
	// will also terminate the container on exit by sending
	// outselves a signal to terminate.
	defer stopThisInstance()

	// If a full Command Line was provided in the request
	// instead of tokens, use shlex to parse it into tokens.
	if len(request.CommandTokens) == 0 && len(request.CommandLine) != 0 {
		tokens, err := shlex.Split(request.CommandLine)
		if err != nil {
			return common.Response{
				Error: fmt.Sprintf("failed to parse command line: %v", err),
			}
		}
		request.CommandTokens = tokens
	}

	var handler CLIDescriptor

	if len(e.Descriptors) == 1 {
		for _, h := range e.Descriptors {
			handler = h
			break
		}
	} else {
		var ok bool
		handler, ok = e.Descriptors[request.Tool]
		if !ok {
			return common.Response{
				Error: fmt.Sprintf("unknown tool: %s", request.Tool),
			}
		}
	}

	resp, err := handler.ProcessCommand(request.CommandTokens, request.Credentials)

	// Log to the adapter.
	anonReq := *request
	anonReq.Credentials = "REDACTED"

	hook := limacharlie.Dict{
		"action":  "run",
		"request": anonReq,
		"by":      ident,
		"inv_id":  invID,
	}

	if err != nil {
		hook["error"] = err.Error()
	}
	if err != errUnknownTool {
		hook["response"] = &resp
	}

	if err := e.extension.SendToWebhookAdapter(o, hook); err != nil {
		e.Logger.Error(fmt.Sprintf("failed to send to webhook adapter: %v", err))
	}

	if err != nil {
		return common.Response{
			Data:  &resp,
			Error: err.Error(),
		}
	}
	return common.Response{Data: &resp}
}

func (e *CLIExtension) TryParsingOutput(output []byte) CLIReturnData {
	// Try to parse multiple dictionaries in a row.
	r := bytes.NewReader(output)
	l := []limacharlie.Dict{}
	for {
		d := limacharlie.Dict{}
		if err := json.NewDecoder(r).Decode(&d); err != nil {
			break
		}
		l = append(l, d)
	}

	if len(l) == 1 {
		return CLIReturnData{OutputDict: l[0]}
	}
	if len(l) > 1 {
		return CLIReturnData{OutputList: l}
	}

	// Is it a list?
	if err := json.Unmarshal(output, &l); err == nil {
		return CLIReturnData{OutputList: l}
	}

	if len(l) > 0 {
		return CLIReturnData{OutputList: l}
	}

	// Just return the original as stdout.
	return CLIReturnData{OutputString: string(output)}
}

func stopThisInstance() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}
	p.Signal(syscall.SIGTERM)
}
