package simplified

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"

	"github.com/refractionPOINT/shlex"
)

type CLIHandler = func(ctx context.Context, cliTokens []string, credentials string) (CLIReturnData, error)

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

// Common errors which can be used by custom CLI extensions to signal specific
// error conditions.
var ErrInvalidCredentials = errors.New("invalid credentials")

// CommandError represents an error when a specific CLI command is not allowed.
type CommandError struct {
	Command string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s command not allowed", e.Command)
}

func NewCommandError(command string) error {
	return &CommandError{Command: command}
}

// Timeout for CLI command execution
const toolCommandExecutionTimeout = 9 * time.Minute

// Maximum size / length for the CLI arguments in bytes
const commandArgumentsMaxSize = 1024 * 10

// Maximum number of items for CLI arguments when specified as a list / parsing string argument to a list
const commandArgumentsMaxCount = 50

// Default implementation of SendToWebhookAdapterFunc. Only to be overridden by tests.
var sendToWebhookAdapterFunc = func(ext *core.Extension, o *limacharlie.Organization, hook limacharlie.Dict) error {
	return ext.SendToWebhookAdapter(o, hook)
}

// Default implementation of stopThisInstance. Only to be overridden by tests.
var stopThisInstanceFunc = func(logger limacharlie.LCLogger, o *limacharlie.Organization, request *CLIRunRequest, error string) {
	if error == "" {
		logger.Info(fmt.Sprintf("stopping instance after successful processing for oid %s and tool %s", o.GetOID(), request.Tool))
	} else {
		logger.Info(fmt.Sprintf("stopping instance after failed processing for oid %s and tool %s: %s", o.GetOID(), request.Tool, error))
	}

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		logger.Error(fmt.Sprintf("failed to find process: %v", err))
		return
	}

	// Send SIGTERM to the current process.
	if err := p.Signal(syscall.SIGTERM); err != nil {
		logger.Error(fmt.Sprintf("failed to send SIGTERM: %v", err))
		return
	}

	// TODO: Should we add a safe guard and send SIGKILL if process is still alive after
	// X seconds?
}

func Bool(v bool) *bool {
	return &v
}

func (e *CLIExtension) Init() (*core.Extension, error) {
	isSingleTool := len(e.Descriptors) == 1

	requiredFields := [][]common.SchemaKey{{"command_tokens", "command_line"}, {"credentials"}}
	if !isSingleTool {
		requiredFields = append(requiredFields, []common.SchemaKey{"tool"})
	}
	toolList := []interface{}{}
	for k := range e.Descriptors {
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
		ExtensionName: e.Name,
		SecretKey:     e.SecretKey,
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
						"command_line": {
							DataType:     common.SchemaDataTypes.String,
							Label:        "Command Line",
							Description:  "The command to run.",
							IsList:       false,
							DisplayIndex: 3,
						},
						"command_tokens": {
							DataType:     common.SchemaDataTypes.String,
							Label:        "Command Parameters",
							Description:  "The command parameters to run as a tokenized list.",
							IsList:       true,
							DisplayIndex: 4,
						},
						"credentials": {
							DataType:     common.SchemaDataTypes.Secret,
							Label:        "Credentials",
							Description:  `The credentials to use for the command. A GCP JSON key, a DigitalOcean Access Token or an AWS "accessKeyID/secretAccessKey" pair.`,
							DisplayIndex: 1,
						},
					},
				},
				ResponseDefinition: &common.SchemaObject{
					Fields: map[common.SchemaKey]common.SchemaElement{
						"output_list": {
							DataType:    common.SchemaDataTypes.Object,
							Label:       "Outputs",
							Description: "The output JSON objects of the command.",
							IsList:      true,
						},
						"output_dict": {
							DataType:    common.SchemaDataTypes.Object,
							Label:       "Output",
							Description: "The output JSON object of the command.",
							IsList:      false,
						},
						"output_string": {
							DataType:    common.SchemaDataTypes.String,
							Label:       "Raw Output",
							Description: "The non-JSON output of the command.",
						},
						"status_code": {
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
			e.Logger.Info(fmt.Sprintf("validate config from %s", org.GetOID()))
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"run": {
				RequestStruct: &CLIRunRequest{},
				Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
					return e.doRun(params.Org, params.Request.(*CLIRunRequest), params.Ident, params.InvestigationID)
				},
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				e.Logger.Info(fmt.Sprintf("subscribe to %s", params.Org.GetOID()))
				if err := e.installRulesIfNeeded(params.Org); err != nil {
					e.Logger.Error(fmt.Sprintf("failed to install rules: %v", err))
					return common.Response{
						Error: err.Error(),
					}
				}
				return common.Response{}
			},
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				e.Logger.Info(fmt.Sprintf("unsubscribe from %s", params.Org.GetOID()))
				if err := e.uninstallAllRules(params.Org); err != nil {
					e.Logger.Error(fmt.Sprintf("failed to uninstall rules: %v", err))
					return common.Response{
						Error: err.Error(),
					}
				}
				return common.Response{}
			},
		},
		ErrorHandler: func(erm *common.ErrorReportMessage) {
			e.Logger.Error(fmt.Sprintf("received error from LC for %s: %s", erm.Oid, erm.Error))
		},
	}

	e.extension = x

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
	var doRunResp common.Response

	defer func() {
		e.stopThisInstance(o, request, doRunResp.Error)
	}()

	e.Logger.Debug(fmt.Sprintf("running command for %s and tool %s", o.GetOID(), request.Tool))

	// If a full Command Line was provided in the request
	// instead of tokens, use shlex to parse it into tokens.
	if len(request.CommandTokens) == 0 && len(request.CommandLine) != 0 {
		tokens, err := shlex.Split(request.CommandLine)
		if err != nil {
			e.Logger.Info(fmt.Sprintf("failed to parse command line for %s and tool %s: %v", o.GetOID(), request.Tool, err))
			doRunResp = common.Response{
				Error:     fmt.Sprintf("failed to parse command line: %v", err),
				Retriable: Bool(false),
			}
			return doRunResp
		}
		request.CommandTokens = tokens
	}

	if len(request.CommandLine) > commandArgumentsMaxSize {
		e.Logger.Info(fmt.Sprintf("command line is too long for %s and tool %s, got %d, max size is %d", o.GetOID(), request.Tool, len(request.CommandLine), commandArgumentsMaxSize))
		doRunResp = common.Response{
			Error:     fmt.Sprintf("command line is too long, max size is %d bytes", commandArgumentsMaxSize),
			Retriable: Bool(false),
		}
		return doRunResp
	}

	if len(request.CommandTokens) > commandArgumentsMaxCount {
		e.Logger.Info(fmt.Sprintf("command arguments are too long for %s and tool %s, got %d, max count is %d", o.GetOID(), request.Tool, len(request.CommandTokens), commandArgumentsMaxCount))
		doRunResp = common.Response{
			Error:     fmt.Sprintf("command arguments are too long, max count is %d", commandArgumentsMaxCount),
			Retriable: Bool(false),
		}
		return doRunResp
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
			e.Logger.Info(fmt.Sprintf("unknown tool for %s: %s", o.GetOID(), request.Tool))
			doRunResp = common.Response{
				Error:     fmt.Sprintf("unknown tool: %s", request.Tool),
				Retriable: Bool(false),
			}
			return doRunResp
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), toolCommandExecutionTimeout)
	defer cancel()
	start := time.Now()
	resp, err := handler.ProcessCommand(ctx, request.CommandTokens, request.Credentials)
	elapsed := time.Since(start)

	// Log to the adapter.
	anonReq := *request
	anonReq.Credentials = "REDACTED"
	anonReq.CommandLine = core.MaskSecrets(request.CommandLine, []string{request.Credentials})
	anonReq.CommandTokens = core.MaskSecretsInSlice(request.CommandTokens, []string{request.Credentials})

	hook := limacharlie.Dict{
		"action":  "run",
		"request": anonReq,
		"by":      ident,
		"inv_id":  invID,
	}

	if err != nil {
		hook["error"] = err.Error()

		if err != errUnknownTool {
			// Include additional context in the webhook payload
			hook["response"] = &resp
		}

		// Those usually don't represent fatal erros so we log them under info
		// It's important that error message doesn't contain any secrets such as potential
		// CLI arguments, credentials, etc.
		e.Logger.Info(fmt.Sprintf("command for %s and tool %s failed and took %f seconds: %v", o.GetOID(), request.Tool, elapsed.Seconds(), err))
	} else {
		e.Logger.Debug(fmt.Sprintf("command for %s and tool %s succeeded and took %f seconds", o.GetOID(), request.Tool, elapsed.Seconds()))
	}

	if err := sendToWebhookAdapterFunc(e.extension, o, hook); err != nil {
		e.Logger.Error(fmt.Sprintf("failed to send to webhook adapter: %v", err))
	}

	if err != nil {
		doRunResp = common.Response{
			Data:      &resp,
			Error:     err.Error(),
			Retriable: Bool(isErrorRetriable(err)),
		}
		return doRunResp
	}
	doRunResp = common.Response{Data: &resp}
	return doRunResp
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

func (e *CLIExtension) stopThisInstance(o *limacharlie.Organization, request *CLIRunRequest, error string) {
	stopThisInstanceFunc(e.Logger, o, request, error)
}

// Return true if a specific error is considered retriable.
func isErrorRetriable(err error) bool {
	if err == nil {
		return false
	}
	// For the time being, we retry all other errors exception context timeout, canceled, invalid credentials
	// and common command errors which are not retriable.
	isRetriable := true

	var cmdErr *CommandError
	if errors.Is(err, context.DeadlineExceeded) {
		isRetriable = false
	} else if errors.Is(err, context.Canceled) {
		isRetriable = false
	} else if errors.Is(err, ErrInvalidCredentials) {
		isRetriable = false
	} else if errors.As(err, &cmdErr) {
		isRetriable = false
	}

	return isRetriable
}
