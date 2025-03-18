package simplified

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/core"
)

// dummyLogger is a minimal implementation of limacharlie.LCLogger.
type dummyLogger struct{}

func (d dummyLogger) Info(_ string)  {}
func (d dummyLogger) Error(_ string) {}
func (d dummyLogger) Debug(_ string) {}
func (d dummyLogger) Warn(_ string)  {}
func (d dummyLogger) Fatal(_ string) {}
func (d dummyLogger) Trace(_ string) {}

// dummyClientOptions holds minimal options to satisfy the client requirements.
var dummyOpt = limacharlie.ClientOptions{
	OID: "572d1b12-158c-4b86-87cd-554850b346cd",
}

var dummyCoreExt = &core.Extension{
	ExtensionName: "dummy-name",
	SecretKey:     "dummy",
}

func TestDoRun_ErrorHandling(t *testing.T) {
	sendToWebhookCalled := false
	stopThisInstanceCalled := false

	originalSendToWebhook := sendToWebhookAdapterFunc
	originalStopThisInstance := stopThisInstanceFunc

	defer func() {
		sendToWebhookAdapterFunc = originalSendToWebhook
		stopThisInstanceFunc = originalStopThisInstance
	}()

	sendToWebhookAdapterFunc = func(ext *core.Extension, o *limacharlie.Organization, hook limacharlie.Dict) error {
		if ext.ExtensionName != "dummy-name" {
			t.Errorf("expected extension name 'test-extension', got %s", ext.ExtensionName)
		}

		if o.GetOID() != dummyOpt.OID {
			t.Errorf("expected OID %s, got %s", dummyOpt.OID, o.GetOID())
		}

		if hook["action"] != "run" {
			t.Errorf("expected action 'run', got %s", hook["action"])
		}

		if hook["inv_id"] != "inv" {
			t.Errorf("expected inv_ident 'inv', got %s", hook["inv_ident"])
		}

		// Verify credentials are masked in various request fields
		hookRequest, ok := hook["request"].(CLIRunRequest)
		if !ok {
			t.Fatalf("expected hook request to be of type CLIRunRequest, got: %T", hook["request"])
		}

		if hookRequest.Credentials != "REDACTED" {
			t.Errorf("expected credentials to be 'REDACTED', but got: %s", hookRequest.Credentials)
		}

		if strings.Contains(hookRequest.CommandLine, "linewithsecret") && !strings.Contains(hookRequest.CommandLine, "REDACTED") {
			t.Errorf("expected command line to be redacted, but it wasn't")
		}

		if strings.Contains(strings.Join(hookRequest.CommandTokens, "argwithsecret"), " ") && !strings.Contains(strings.Join(hookRequest.CommandTokens, " "), "REDACTED") {
			t.Errorf("expected command line to be redacted, but it wasn't")
		}

		sendToWebhookCalled = true
		return nil
	}
	stopThisInstanceFunc = func(logger limacharlie.LCLogger, o *limacharlie.Organization, req *CLIRunRequest, errMsg string) {
		if o.GetOID() != dummyOpt.OID {
			t.Errorf("expected OID %s, got %s", dummyOpt.OID, o.GetOID())
		}

		stopThisInstanceCalled = true
	}

	// Create a dummy organization and logger.
	org, err := limacharlie.NewOrganizationFromClientOptions(dummyOpt, dummyLogger{})
	if err != nil {
		t.Fatalf("failed to create organization: %v", err)
	}
	log := dummyLogger{}

	assertCommonHandlerArguments := func(ctx context.Context, tokens []string, creds string) {
		if ctx == nil {
			t.Errorf("expected context to be non-nil")
		}
		if len(tokens) == 0 {
			t.Errorf("expected tokens to be non-empty")
		}
		if creds == "" {
			t.Errorf("expected creds to be non-empty")
		}
	}
	// Dummy CLIHandlers to simulate different error conditions.
	dummyHandlerSuccess := func(ctx context.Context, tokens []string, creds string) (CLIReturnData, error) {
		assertCommonHandlerArguments(ctx, tokens, creds)
		return CLIReturnData{OutputString: "success"}, nil
	}
	dummyHandlerDeadline := func(ctx context.Context, tokens []string, creds string) (CLIReturnData, error) {
		assertCommonHandlerArguments(ctx, tokens, creds)
		return CLIReturnData{}, context.DeadlineExceeded
	}
	dummyHandlerCanceled := func(ctx context.Context, tokens []string, creds string) (CLIReturnData, error) {
		assertCommonHandlerArguments(ctx, tokens, creds)
		return CLIReturnData{}, context.Canceled
	}
	dummyHandlerGeneric := func(ctx context.Context, tokens []string, creds string) (CLIReturnData, error) {
		assertCommonHandlerArguments(ctx, tokens, creds)
		return CLIReturnData{}, errors.New("generic error")
	}

	// Start with a CLIExtension that has one descriptor ("dummy").
	cliExt := &CLIExtension{
		Name:        "test-extension",
		SecretKey:   "secret",
		Logger:      log,
		Descriptors: map[CLIName]CLIDescriptor{"dummy": {ProcessCommand: dummyHandlerSuccess, CredentialsFormat: "", ExampleCommand: "dummy"}},
		extension:   dummyCoreExt,
	}

	// Test case: invalid command line (shlex.Split error).
	t.Run("invalid command line", func(t *testing.T) {
		// An unmatched quote should cause shlex.Split to return an error.
		req := &CLIRunRequest{
			CommandLine:   `echo "unmatched quote`,
			CommandTokens: []string{},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		if !strings.Contains(resp.Error, "failed to parse command line") {
			t.Errorf("expected parse error, got: %s", resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false")
		}
	})

	// Test case: command line too long.
	t.Run("command line too long", func(t *testing.T) {
		longCmd := strings.Repeat("a", commandArgumentsMaxSize+1)
		req := &CLIRunRequest{
			CommandLine:   longCmd,
			CommandTokens: []string{},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		expected := fmt.Sprintf("command line is too long, max size is %d bytes", commandArgumentsMaxSize)
		if resp.Error != expected {
			t.Errorf("expected error: %s, got: %s", expected, resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false")
		}
	})

	// Test case: command tokens too many.
	t.Run("command tokens too many", func(t *testing.T) {
		tokens := make([]string, commandArgumentsMaxCount+1)
		req := &CLIRunRequest{
			CommandLine:   "",
			CommandTokens: tokens,
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		expected := fmt.Sprintf("command arguments are too long, max count is %d", commandArgumentsMaxCount)
		if resp.Error != expected {
			t.Errorf("expected error: %s, got: %s", expected, resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false")
		}
	})

	// Test case: unknown tool.
	t.Run("unknown tool", func(t *testing.T) {
		// Create a CLIExtension with multiple tools.
		cliExtMulti := &CLIExtension{
			Name:      "test-extension",
			SecretKey: "secret",
			Logger:    log,
			Descriptors: map[CLIName]CLIDescriptor{
				"tool1": {ProcessCommand: dummyHandlerSuccess, CredentialsFormat: "", ExampleCommand: "cmd"},
				"tool2": {ProcessCommand: dummyHandlerSuccess, CredentialsFormat: "", ExampleCommand: "cmd"},
			},
			extension: dummyCoreExt,
		}
		req := &CLIRunRequest{
			CommandLine:   "cmd",
			CommandTokens: []string{"cmd"},
			Credentials:   "creds",
			Tool:          "nonexistent",
		}
		resp := cliExtMulti.doRun(org, req, "ident", "inv")
		expected := "unknown tool: nonexistent"
		if resp.Error != expected {
			t.Errorf("expected error: %s, got: %s", expected, resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false")
		}
	})

	// Test case: ProcessCommand returns context.DeadlineExceeded.
	t.Run("ProcessCommand context.DeadlineExceeded", func(t *testing.T) {
		cliExt.Descriptors["dummy"] = CLIDescriptor{ProcessCommand: dummyHandlerDeadline, CredentialsFormat: "", ExampleCommand: "cmd"}
		req := &CLIRunRequest{
			CommandLine:   "cmd",
			CommandTokens: []string{"cmd"},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		// Expect error message to include DeadlineExceeded.
		if !strings.Contains(resp.Error, "context deadline exceeded") {
			t.Errorf("expected DeadlineExceeded error, got: %s", resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false for DeadlineExceeded")
		}
	})

	// Test case: ProcessCommand returns context.Canceled.
	t.Run("ProcessCommand context.Canceled", func(t *testing.T) {
		cliExt.Descriptors["dummy"] = CLIDescriptor{ProcessCommand: dummyHandlerCanceled, CredentialsFormat: "", ExampleCommand: "cmd"}
		req := &CLIRunRequest{
			CommandLine:   "cmd",
			CommandTokens: []string{"cmd"},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		// Expect error message to include "canceled" (case-insensitive check).
		if !strings.Contains(strings.ToLower(resp.Error), "canceled") {
			t.Errorf("expected canceled error, got: %s", resp.Error)
		}
		if resp.Retriable == nil || *resp.Retriable {
			t.Errorf("expected retriable to be false for canceled error")
		}
	})

	// Test case: ProcessCommand returns a generic error.
	t.Run("ProcessCommand generic error", func(t *testing.T) {
		cliExt.Descriptors["dummy"] = CLIDescriptor{ProcessCommand: dummyHandlerGeneric, CredentialsFormat: "", ExampleCommand: "cmd"}
		req := &CLIRunRequest{
			CommandLine:   "cmd",
			CommandTokens: []string{"cmd"},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		if resp.Error != "generic error" {
			t.Errorf("expected generic error, got: %s", resp.Error)
		}
		if resp.Retriable == nil || !*resp.Retriable {
			t.Errorf("expected retriable to be true for generic error")
		}
		if !sendToWebhookCalled {
			t.Errorf("expected sendToWebhookAdapterFunc to be called, but it wasn't")
		}
		if !stopThisInstanceCalled {
			t.Errorf("expected stopThisInstanceFunc to be called, but it wasn't")
		}
	})

	// Test case: ProcessCommand returns a generic error, which should be retriable.
	t.Run("ProcessCommand generic retriable error", func(t *testing.T) {
		cliExt.Descriptors["dummy"] = CLIDescriptor{ProcessCommand: func(ctx context.Context, tokens []string, creds string) (CLIReturnData, error) {
			return CLIReturnData{}, errors.New("temporary error")
		}, CredentialsFormat: "", ExampleCommand: "cmd"}

		req := &CLIRunRequest{
			CommandLine:   "cmd",
			CommandTokens: []string{"cmd"},
			Credentials:   "creds",
			Tool:          "dummy",
		}

		resp := cliExt.doRun(org, req, "ident", "inv")

		// Expect the error message to be "temporary error"
		if resp.Error != "temporary error" {
			t.Errorf("expected error: 'temporary error', got: %s", resp.Error)
		}

		// Check that the error is retriable
		if resp.Retriable == nil || !*resp.Retriable {
			t.Errorf("expected retriable to be true for generic retriable error")
		}
	})

	// Test case: successful ProcessCommand execution.
	t.Run("ProcessCommand success", func(t *testing.T) {
		cliExt.Descriptors["dummy"] = CLIDescriptor{ProcessCommand: dummyHandlerSuccess, CredentialsFormat: "", ExampleCommand: "cmd"}
		req := &CLIRunRequest{
			CommandLine:   "cmd creds linewithsecret",
			CommandTokens: []string{"cmd", "creds", "argwithsecret"},
			Credentials:   "creds",
			Tool:          "dummy",
		}
		resp := cliExt.doRun(org, req, "ident", "inv")
		if resp.Error != "" {
			t.Errorf("expected no error, got: %s", resp.Error)
		}
		// Verify that the returned data contains the expected output.
		data, ok := resp.Data.(*CLIReturnData)
		if !ok || data.OutputString != "success" {
			t.Errorf("expected output 'success', got: %v", resp.Data)
		}
	})
}
