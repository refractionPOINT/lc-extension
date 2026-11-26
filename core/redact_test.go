package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

// Obviously fake values: none is a real key or token.
const (
	fakeSecretParam = "FAKE-SECRET-PARAM-VALUE-0001"
	fakeOrgJWT      = "FAKE-ORG-JWT-VALUE-0002"
)

// A request callback that echoes a Secret parameter in its error, or panics
// with it, gets it into neither the error report nor the HTTP answer.
func TestCallbackErrorAndPanicCarryNoSecret(t *testing.T) {
	for name, cb := range map[string]func(ctx context.Context, params RequestCallbackParams) common.Response{
		"error": func(ctx context.Context, params RequestCallbackParams) common.Response {
			d := params.Request.(limacharlie.Dict)
			return common.Response{Error: "bad key " + d["key"].(string) + " for " + fakeOrgJWT}
		},
		"panic": func(ctx context.Context, params RequestCallbackParams) common.Response {
			d := params.Request.(limacharlie.Dict)
			panic("bad key " + d["key"].(string))
		},
	} {
		t.Run(name, func(t *testing.T) {
			ext, ms := newTestExtension(t)
			defer ms.Close()
			var reports []string
			ext.Callbacks.ErrorHandler = func(r *common.ErrorReportMessage) { reports = append(reports, r.Error) }
			ext.isLogAllErrors = true
			ext.RequestSchema = common.RequestSchemas{"echo": {ParameterDefinitions: common.SchemaObject{Fields: map[string]common.SchemaElement{
				"key": {DataType: common.SchemaDataTypes.Secret},
			}}}}
			ext.Callbacks.RequestHandlers = map[common.ActionName]RequestCallback{"echo": {Callback: cb}}

			body, _ := json.Marshal(&common.Message{Version: PROTOCOL_VERSION, Request: &common.RequestMessage{
				Org:    common.OrgAccessData{OID: "oid-test", JWT: fakeOrgJWT},
				Action: "echo",
				Data:   limacharlie.Dict{"key": fakeSecretParam},
			}})
			mac := hmac.New(sha256.New, []byte(testSecret))
			mac.Write(body)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
			req.Header.Set("lc-ext-sig", hex.EncodeToString(mac.Sum(nil)))
			rec := httptest.NewRecorder()
			ext.ServeHTTP(rec, req)

			captured := rec.Body.String() + strings.Join(reports, "\n")
			if len(reports) == 0 || rec.Code < 400 {
				t.Fatalf("expected the failure answered and reported: %d %s", rec.Code, captured)
			}
			for _, v := range []string{fakeSecretParam, fakeOrgJWT} {
				if strings.Contains(captured, v) {
					t.Fatalf("a secret reached the answer or the report: %s", captured)
				}
			}
		})
	}
}

func TestScrubSecretsShortValueWithholdsText(t *testing.T) {
	if got := scrubSecrets("pin 123 refused", []string{"123"}); strings.Contains(got, "123") {
		t.Fatalf("a short secret survived: %q", got)
	}
}
