package core

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

// The rule that issued a request is platform-set data: the handler must see it, and it must survive
// being re-wrapped for another extension (the multiplexer), or a forwarded action would silently lose
// the attribution it was sent with.
func TestRequestHandlerReceivesAutomationRule(t *testing.T) {
	ext, ms := newTestExtension(t)
	defer ms.Close()

	params, rec := serveRequest(t, ext, common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		Request: &common.RequestMessage{
			Org:            common.OrgAccessData{OID: "oid-test", JWT: "jwt", Ident: "DR:general.rule"},
			Action:         testAction,
			Data:           limacharlie.Dict{"automation_rule": "forged"}, // user-controlled, must be ignored
			Config:         limacharlie.Dict{},
			AutomationRule: "general.rule",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if params.AutomationRule != "general.rule" {
		t.Fatalf("AutomationRule = %q, want the platform-set value", params.AutomationRule)
	}
	fwd := params.ToRequestMessage("other", common.OrgAccessData{OID: "oid-test"})
	if fwd.AutomationRule != "general.rule" {
		t.Fatalf("forwarded AutomationRule = %q, want it carried over", fwd.AutomationRule)
	}

	plain, _ := serveRequest(t, ext, common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		Request: &common.RequestMessage{
			Org:    common.OrgAccessData{OID: "oid-test", JWT: "jwt", Ident: testIdent},
			Action: testAction,
			Data:   limacharlie.Dict{"automation_rule": "forged"},
			Config: limacharlie.Dict{},
		},
	})
	if plain.AutomationRule != "" {
		t.Fatalf("a request the platform did not mark as automation carried AutomationRule %q", plain.AutomationRule)
	}
}

// The manager learns the flag from the schema the extension serves, so the wire name is a contract.
func TestRequestSchemaAllowAutomationWireName(t *testing.T) {
	b, err := json.Marshal(common.RequestSchema{IsImpersonated: true, AllowAutomation: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["allow_automation"] != true || m["is_impersonated"] != true {
		t.Fatalf("schema wire form = %s", b)
	}
}
