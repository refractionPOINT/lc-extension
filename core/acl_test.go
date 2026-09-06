package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

const (
	testSecret = "test-secret"
	testAction = "ping"
	testIdem   = "idem"
	testIdent  = "someone@example.com"
)

// serveRequest signs and posts a request envelope to the extension and returns
// the params the "ping" handler was invoked with.
func serveRequest(t *testing.T, ext *Extension, msg common.Message) (RequestCallbackParams, *httptest.ResponseRecorder) {
	t.Helper()
	var got RequestCallbackParams
	ext.Callbacks.RequestHandlers = map[common.ActionName]RequestCallback{
		testAction: {
			RequestStruct: nil,
			Callback: func(ctx context.Context, params RequestCallbackParams) common.Response {
				got = params
				return common.Response{Data: limacharlie.Dict{"pong": true}}
			},
		},
	}
	body, err := json.Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("lc-ext-sig", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	ext.ServeHTTP(rec, req)
	return got, rec
}

func newTestExtension(t *testing.T) (*Extension, *limacharlie.MockServer) {
	t.Helper()
	ms := limacharlie.NewMockServer("oid-test")
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatal(err)
	}
	ext := &Extension{
		ExtensionName: "acl-test",
		SecretKey:     testSecret,
		OrgFromAccess: func(common.OrgAccessData) (*limacharlie.Organization, error) { return org, nil },
		Callbacks: ExtensionCallbacks{
			ErrorHandler: func(*common.ErrorReportMessage) {},
		},
	}
	if err := ext.Init(); err != nil {
		t.Fatal(err)
	}
	return ext, ms
}

func TestRequestHandlerReceivesACLBlock(t *testing.T) {
	ext, ms := newTestExtension(t)
	defer ms.Close()

	block := &common.ACL{Scopes: []string{"hr", "finance"}, Global: false, Enforce: true, Source: common.ACLSourceUser}
	params, rec := serveRequest(t, ext, common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		Request: &common.RequestMessage{
			Org:    common.OrgAccessData{OID: "oid-test", JWT: "jwt", Ident: testIdent},
			Action: testAction,
			Data:   limacharlie.Dict{"acl": map[string]interface{}{"global": true}}, // user-controlled, must be ignored
			Config: limacharlie.Dict{},
			ACL:    block,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !params.ACL.Present() {
		t.Fatal("ACL block should be present")
	}
	if !reflect.DeepEqual(params.ACL.Envelope(), block) {
		t.Fatalf("ACL = %+v, want %+v", params.ACL.Envelope(), block)
	}
	if params.ACL.Global {
		t.Fatal("global flag inside data must never be honored")
	}
	if !params.ACL.Allows([]string{"acl:hr"}) || params.ACL.Allows([]string{"acl:legal"}) {
		t.Fatal("Allows does not reflect the scopes in the envelope")
	}
	if params.Ident != testIdent {
		t.Fatalf("Ident = %q", params.Ident)
	}
}

func TestRequestHandlerWithoutACLBlockFailsClosed(t *testing.T) {
	ext, ms := newTestExtension(t)
	defer ms.Close()

	params, rec := serveRequest(t, ext, common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		Request: &common.RequestMessage{
			Org:    common.OrgAccessData{OID: "oid-test", JWT: "jwt"},
			Action: testAction,
			Data:   limacharlie.Dict{},
			Config: limacharlie.Dict{},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if params.ACL.Present() {
		t.Fatal("ACL block should be absent")
	}
	if !params.ACL.Enforce || params.ACL.Global || params.ACL.Scopes != nil {
		t.Fatalf("absent block must fail closed, got %+v", params.ACL)
	}
	if !params.ACL.Allows([]string{"prod"}) {
		t.Fatal("untagged resources must be allowed when the block is absent")
	}
	if params.ACL.Allows([]string{"acl:hr"}) {
		t.Fatal("acl-tagged resources must be locked when the block is absent")
	}
}

func TestToRequestMessageForwardsEnvelopeUnchanged(t *testing.T) {
	block := &common.ACL{Scopes: []string{"hr"}, Global: false, Enforce: true, Source: common.ACLSourceContinuation}
	params := RequestCallbackParams{
		Ident:           testIdent,
		Request:         limacharlie.Dict{"k": "v"},
		Config:          limacharlie.Dict{"c": "d"},
		IdempotentKey:   testIdem,
		ResourceState:   map[string]common.ResourceState{"r": {LastModified: 7}},
		InvestigationID: "inv-1",
		ACL:             common.NewACLView(block),
	}
	oad := common.OrgAccessData{OID: "oid-1", JWT: "fwd-jwt", Ident: params.Ident}
	got := params.ToRequestMessage(testAction, oad)
	want := &common.RequestMessage{
		Org:             oad,
		Action:          testAction,
		Data:            limacharlie.Dict{"k": "v"},
		Config:          limacharlie.Dict{"c": "d"},
		ResourceState:   map[string]common.ResourceState{"r": {LastModified: 7}},
		InvestigationID: "inv-1",
		ACL:             block,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToRequestMessage = %+v, want %+v", got, want)
	}

	// The forwarded envelope must serialize with the block at the top level
	// so the worker's SDK sees it as present.
	b, err := json.Marshal(&common.Message{Version: PROTOCOL_VERSION, Request: got})
	if err != nil {
		t.Fatal(err)
	}
	back := common.Message{}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !common.NewACLView(back.Request.ACL).Present() || back.Request.Org.Ident != params.Ident {
		t.Fatalf("forwarded envelope lost acl or ident: %s", b)
	}
}

func TestToRequestMessageWithoutACLBlockStaysAbsent(t *testing.T) {
	params := RequestCallbackParams{
		Request: limacharlie.Dict{},
		ACL:     common.NewACLView(nil),
	}
	got := params.ToRequestMessage(testAction, common.OrgAccessData{OID: "oid-1"})
	if got.ACL != nil {
		t.Fatalf("absent block must be forwarded as absent, got %+v", got.ACL)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"acl"`) {
		t.Fatalf("absent block must not be serialized: %s", b)
	}
}
