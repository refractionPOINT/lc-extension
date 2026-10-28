package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

// A config-validation message carries its org in ConfigValidation, not in
// Request. When the org client can't be built, the extension must answer 500
// and report the error against that org instead of crashing on the nil Request.
func TestConfigValidationOrgClientFailureReports500(t *testing.T) {
	// With LC_EXTENSION_LOG_ALL_ERRORS set, respondAndLog adds a second report.
	t.Setenv("LC_EXTENSION_LOG_ALL_ERRORS", "")
	var reports []*common.ErrorReportMessage
	ext := &Extension{
		ExtensionName: "conf-validation-test",
		SecretKey:     testSecret,
		OrgFromAccess: func(common.OrgAccessData) (*limacharlie.Organization, error) {
			return nil, errors.New("jwt exchange failed")
		},
		Callbacks: ExtensionCallbacks{
			ErrorHandler: func(r *common.ErrorReportMessage) { reports = append(reports, r) },
			ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
				t.Fatal("ValidateConfig must not run without an org client")
				return common.Response{}
			},
		},
	}
	if err := ext.Init(); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(&common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		ConfigValidation: &common.ConfigValidationMessage{
			Org:    common.OrgAccessData{OID: "oid-conf", JWT: "jwt"},
			Config: limacharlie.Dict{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("lc-ext-sig", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	ext.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp common.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response body %q: %v", rec.Body.String(), err)
	}
	if !strings.Contains(resp.Error, "jwt exchange failed") {
		t.Fatalf("response error = %q", resp.Error)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d error reports, want 1", len(reports))
	}
	if reports[0].Oid != "oid-conf" || !strings.Contains(reports[0].Error, "jwt exchange failed") {
		t.Fatalf("error report = %+v", reports[0])
	}
}
