package multiplexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

// A worker answer that does not decode can echo the request it was sent,
// Secret parameters included: the error built from it must not carry it.
func TestForwardHTTPDecodeErrorCarriesNoBody(t *testing.T) {
	const fakeSecret = "FAKE-SECRET-PARAM-VALUE-0001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>upstream echoed " + fakeSecret + "</html>"))
	}))
	defer server.Close()

	_, err := forwardHTTP(context.Background(), []byte("s"), server.Client(), server.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if strings.Contains(err.Error(), fakeSecret) {
		t.Fatalf("the error carries the body: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected the status in the error: %v", err)
	}
}

// A worker echoing a Secret value it was forwarded (as put in by a hook) in
// its error, plainly or JSON-escaped, gets it scrubbed from what the
// multiplexer answers.
func TestForwardScrubbedScrubsWorkerError(t *testing.T) {
	const exchanged = "FAKE\"EXCHANGED-JWT-0002"
	b, _ := json.Marshal(exchanged)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(common.Response{Error: "auth failed " + exchanged + " / " + string(b)})
		w.Write(resp)
	}))
	defer server.Close()
	m := &Multiplexer{httpClient: server.Client()}
	m.RequestSchema = common.RequestSchemas{"run": {ParameterDefinitions: common.SchemaObject{Fields: map[string]common.SchemaElement{
		"credentials": {DataType: common.SchemaDataTypes.Secret},
	}}}}
	msg := &common.Message{Request: &common.RequestMessage{Action: "run", Data: limacharlie.Dict{"credentials": exchanged}}}
	resp, err := m.forwardScrubbed(context.Background(), []byte("s"), server.URL, msg, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp.Error, "EXCHANGED") || !strings.Contains(resp.Error, "[REDACTED]") {
		t.Fatalf("the worker's echo survived: %q", resp.Error)
	}
}
