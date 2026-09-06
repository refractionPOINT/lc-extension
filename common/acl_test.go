package common

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/vmihailenco/msgpack/v5"
)

func TestACLViewAllows(t *testing.T) {
	enforcing := func(scopes ...string) ACLView {
		return NewACLView(&ACL{Scopes: scopes, Enforce: true, Source: ACLSourceUser})
	}
	tests := []struct {
		name string
		view ACLView
		tags []string
		want bool
	}{
		{"untagged resource", enforcing("hr"), []string{"prod", "windows"}, true},
		{"no tags at all", enforcing("hr"), nil, true},
		{"member of the scope", enforcing("hr"), []string{"acl:hr"}, true},
		{"not a member", enforcing("hr"), []string{"acl:finance"}, false},
		{"AND over two scopes, both held", enforcing("hr", "finance"), []string{"acl:hr", "acl:finance"}, true},
		{"AND over two scopes, one missing", enforcing("hr"), []string{"acl:hr", "acl:finance"}, false},
		{"unknown scope locks", enforcing("hr"), []string{"acl:does-not-exist"}, false},
		{"bare acl: locks", enforcing("hr"), []string{"acl:"}, false},
		{"bare acl: locks even with whitespace", enforcing("hr"), []string{" ACL:  "}, false},
		{"mixed case and whitespace on tag", enforcing("hr"), []string{"  ACL:HR  "}, true},
		{"mixed case and whitespace on scope", enforcing("  HR "), []string{"acl:hr"}, true},
		{"comma-joined entry, all held", enforcing("hr", "finance"), []string{"prod,acl:hr, acl:finance"}, true},
		{"comma-joined entry, one missing", enforcing("hr"), []string{"acl:hr,acl:finance"}, false},
		{"non-acl tags mixed in are ignored", enforcing("hr"), []string{"prod", "acl:hr", "win"}, true},
		{"prefix must be exact namespace", enforcing("hr"), []string{"acls:hr", "xacl:hr"}, true},
		{"empty scopes with acl tag locks", enforcing(), []string{"acl:hr"}, false},
		{"global allows everything", NewACLView(&ACL{Global: true, Enforce: true}), []string{"acl:hr", "acl:"}, true},
		{"enforce=false allows everything", NewACLView(&ACL{Enforce: false}), []string{"acl:hr", "acl:"}, true},
		{"absent block: untagged allowed", NewACLView(nil), []string{"prod"}, true},
		{"absent block: tagged locked", NewACLView(nil), []string{"acl:hr"}, false},
		{"absent block: bare acl: locked", NewACLView(nil), []string{"acl:"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.view.Allows(tt.tags); got != tt.want {
				t.Fatalf("Allows(%v) = %v, want %v (view=%+v)", tt.tags, got, tt.want, tt.view)
			}
		})
	}
}

func TestACLViewAbsentIsFailClosed(t *testing.T) {
	v := NewACLView(nil)
	if v.Present() {
		t.Fatal("absent block must not be Present()")
	}
	if v.Scopes != nil || v.Global || !v.Enforce || v.Source != "" {
		t.Fatalf("absent block must default to Scopes=nil Global=false Enforce=true, got %+v", v)
	}
	if v.Envelope() != nil {
		t.Fatalf("absent block must have a nil Envelope(), got %+v", v.Envelope())
	}
}

func TestACLViewPresentRoundTrip(t *testing.T) {
	in := &ACL{Scopes: []string{"hr", "finance"}, Global: false, Enforce: true, Source: ACLSourceDR}
	v := NewACLView(in)
	if !v.Present() {
		t.Fatal("block must be Present()")
	}
	if v.Source != ACLSourceDR {
		t.Fatalf("Source not carried: %q", v.Source)
	}
	if got := v.Envelope(); !reflect.DeepEqual(got, in) {
		t.Fatalf("Envelope() = %+v, want %+v", got, in)
	}
	// A block explicitly saying "enforce=false, no scopes" is present and
	// must NOT be mistaken for the fail-closed default.
	v = NewACLView(&ACL{Enforce: false})
	if !v.Present() || v.Enforce {
		t.Fatalf("explicit enforce=false must be present and not enforcing: %+v", v)
	}
}

func TestACLScopeFromTag(t *testing.T) {
	tests := []struct {
		tag   string
		scope string
		ok    bool
	}{
		{"acl:hr", "hr", true},
		{" ACL:HR ", "hr", true},
		{"acl:", "", true},
		{"acl: ", "", true},
		{"prod", "", false},
		{"", "", false},
		{"acls:hr", "", false},
	}
	for _, tt := range tests {
		scope, ok := ACLScopeFromTag(tt.tag)
		if scope != tt.scope || ok != tt.ok {
			t.Errorf("ACLScopeFromTag(%q) = (%q, %v), want (%q, %v)", tt.tag, scope, ok, tt.scope, tt.ok)
		}
	}
}

func sampleRequest(acl *ACL) Message {
	return Message{
		Version:        20221218,
		IdempotencyKey: "idem-1",
		Request: &RequestMessage{
			Org:             OrgAccessData{OID: "oid-1", JWT: "jwt-1", Ident: "someone@example.com"},
			Action:          "ping",
			Data:            limacharlie.Dict{"k": "v"},
			Config:          limacharlie.Dict{"c": "d"},
			ResourceState:   map[string]ResourceState{"r": {LastModified: 42}},
			InvestigationID: "inv-1",
			ACL:             acl,
		},
	}
}

func TestRequestEnvelopeJSONRoundTrip(t *testing.T) {
	withBlock := &ACL{Scopes: []string{"hr", "finance"}, Global: false, Enforce: true, Source: ACLSourceUser}
	for name, acl := range map[string]*ACL{"with block": withBlock, "without block": nil} {
		t.Run(name, func(t *testing.T) {
			in := sampleRequest(acl)
			b, err := json.Marshal(&in)
			if err != nil {
				t.Fatal(err)
			}
			// The block must sit at the top level of the request, never inside data.
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(b, &envelope); err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(envelope["request"], &raw); err != nil {
				t.Fatal(err)
			}
			_, hasACL := raw["acl"]
			if hasACL != (acl != nil) {
				t.Fatalf("top-level acl presence = %v, want %v: %s", hasACL, acl != nil, b)
			}
			var data map[string]interface{}
			if err := json.Unmarshal(raw["data"], &data); err != nil {
				t.Fatal(err)
			}
			if _, leaked := data["acl"]; leaked {
				t.Fatalf("acl must never be inside data: %s", b)
			}

			out := Message{}
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Request.ACL, acl) {
				t.Fatalf("ACL after JSON round trip = %+v, want %+v", out.Request.ACL, acl)
			}
			if out.Request.Org.Ident != in.Request.Org.Ident || out.Request.InvestigationID != in.Request.InvestigationID {
				t.Fatalf("other envelope fields lost in round trip: %+v", out.Request)
			}
		})
	}
}

func TestRequestEnvelopeMsgpackRoundTrip(t *testing.T) {
	withBlock := &ACL{Scopes: []string{"hr"}, Global: true, Enforce: true, Source: ACLSourceImpersonated}
	for name, acl := range map[string]*ACL{"with block": withBlock, "without block": nil} {
		t.Run(name, func(t *testing.T) {
			in := sampleRequest(acl)
			b, err := msgpack.Marshal(&in)
			if err != nil {
				t.Fatal(err)
			}
			out := Message{}
			if err := msgpack.Unmarshal(b, &out); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Request.ACL, acl) {
				t.Fatalf("ACL after msgpack round trip = %+v, want %+v", out.Request.ACL, acl)
			}
			if out.Request.Action != "ping" || out.Request.Org.OID != "oid-1" {
				t.Fatalf("envelope fields lost in msgpack round trip: %+v", out.Request)
			}
		})
	}
}

func TestACLJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(&ACL{Scopes: []string{"hr"}, Global: true, Enforce: true, Source: ACLSourceUser})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"scopes", "global", "enforce", "source"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing wire field %q in %s", k, b)
		}
	}
	if len(m) != 4 {
		t.Fatalf("unexpected wire fields in %s", b)
	}
}
