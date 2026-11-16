package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/refractionPOINT/lc-extension/common"
)

func TestUnrecognizedMessageDoesNotExposeBody(t *testing.T) {
	for _, msg := range []string{
		`{"unexpected":{"jwt":"synthetic-secret-value","content":"private customer text"}}`,
		`{"synthetic-secret-field":"synthetic-secret-value","request":null,"heartbeat":null}`,
		`{}`,
		`null`,
	} {
		for name, gzipped := range encodings {
			t.Run(fmt.Sprintf("%d/%s", len(msg), name), func(t *testing.T) {
				t.Setenv("LC_EXTENSION_LOG_ALL_ERRORS", "true")
				var reports []string
				ext := &Extension{
					SecretKey: testSecret,
					Callbacks: ExtensionCallbacks{ErrorHandler: func(report *common.ErrorReportMessage) {
						reports = append(reports, report.Error)
					}},
				}
				if err := ext.Init(); err != nil {
					t.Fatal(err)
				}
				body := []byte(msg)
				if gzipped {
					body = gz(t, body)
				}
				rec := post(ext, body, sign([]byte(msg)), gzipped)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", rec.Code)
				}
				var response common.Response
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				want := fmt.Sprintf("no recognized section in request (%d bytes)", len(msg))
				if response.Error != want {
					t.Errorf("response error = %q, want %q", response.Error, want)
				}
				if len(reports) != 1 || reports[0] != want {
					t.Errorf("logged errors = %q, want only %q", reports, want)
				}
			})
		}
	}
}
