package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

const (
	fleetAction = "fleet_read"
	fleetOID1   = "0f2c8d7e-1111-4a2b-9c3d-000000000001"
	fleetOID2   = "0f2c8d7e-1111-4a2b-9c3d-000000000002"
)

type fleetData struct {
	Rows bool `json:"rows"`
}

type fleetCall struct {
	calls  int
	params FleetRequestCallbackParams
}

// newFleetExtension is an Extension that declares one fleet action and
// answers it, and a per-organization action that must keep working. Building
// an Organization fails the test: a fleet request never gets one.
func newFleetExtension(t *testing.T, fc *fleetCall) *Extension {
	t.Helper()
	ext := &Extension{
		ExtensionName: "fleet-test",
		SecretKey:     testSecret,
		RequestSchema: common.RequestSchemas{
			fleetAction: {ShortDescription: "fleet", IsFleet: true},
			testAction:  {ShortDescription: "ping", IsUserFacing: true},
		},
		OrgFromAccess: func(common.OrgAccessData) (*limacharlie.Organization, error) {
			t.Error("an Organization was built for a request that must not need one")
			return nil, fmt.Errorf("unexpected")
		},
		Callbacks: ExtensionCallbacks{
			ErrorHandler: func(*common.ErrorReportMessage) {},
			FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{
				fleetAction: {
					RequestStruct: &fleetData{},
					Callback: func(_ context.Context, p FleetRequestCallbackParams) common.Response {
						fc.calls++
						fc.params = p
						return common.Response{Data: limacharlie.Dict{"n": len(p.OIDs)}}
					},
				},
			},
			RequestHandlers: map[common.ActionName]RequestCallback{
				testAction: {Callback: func(context.Context, RequestCallbackParams) common.Response {
					return common.Response{Data: limacharlie.Dict{"pong": true}}
				}},
			},
		},
	}
	if err := ext.Init(); err != nil {
		t.Fatal(err)
	}
	return ext
}

func fleetBody(t *testing.T, fr *common.FleetRequestMessage) []byte {
	t.Helper()
	b, err := json.Marshal(&common.Message{Version: PROTOCOL_VERSION, IdempotencyKey: testIdem, FleetRequest: fr})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func goodFleetRequest() *common.FleetRequestMessage {
	return &common.FleetRequestMessage{
		Action: fleetAction,
		OIDs:   []string{fleetOID1, fleetOID2},
		Data:   limacharlie.Dict{"rows": true},
		Ident:  testIdent,
	}
}

func TestFleetRequestReachesItsHandler(t *testing.T) {
	for name, gzipped := range encodings {
		t.Run(name, func(t *testing.T) {
			fc := &fleetCall{}
			ext := newFleetExtension(t, fc)
			msg := fleetBody(t, goodFleetRequest())
			body := msg
			if gzipped {
				body = gz(t, msg)
			}
			rec := post(ext, body, sign(msg), gzipped)
			if rec.Code != http.StatusOK || fc.calls != 1 {
				t.Fatalf("status %d calls %d: %s", rec.Code, fc.calls, rec.Body.String())
			}
			want := FleetRequestCallbackParams{
				OIDs:          []string{fleetOID1, fleetOID2},
				Request:       &fleetData{Rows: true},
				Ident:         testIdent,
				IdempotentKey: testIdem,
			}
			if !reflect.DeepEqual(fc.params, want) {
				t.Fatalf("params %+v, want %+v", fc.params, want)
			}
			var resp common.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Version != PROTOCOL_VERSION || resp.Error != "" {
				t.Fatalf("response %+v", resp)
			}
			if d, _ := resp.Data.(map[string]interface{}); d["n"] != float64(2) {
				t.Fatalf("response data %+v", resp.Data)
			}
		})
	}
}

// Without a RequestStruct the handler gets the data as a Dict, and a missing
// data object is an empty one.
func TestFleetRequestWithoutStructGetsDict(t *testing.T) {
	fc := &fleetCall{}
	ext := newFleetExtension(t, fc)
	ext.Callbacks.FleetRequestHandlers[fleetAction] = FleetRequestCallback{
		Callback: func(_ context.Context, p FleetRequestCallbackParams) common.Response {
			fc.calls++
			fc.params = p
			return common.Response{}
		},
	}
	fr := goodFleetRequest()
	fr.Data = nil
	msg := fleetBody(t, fr)
	if rec := post(ext, msg, sign(msg), false); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if d, ok := fc.params.Request.(limacharlie.Dict); !ok || d == nil || len(d) != 0 {
		t.Fatalf("request %#v, want an empty Dict", fc.params.Request)
	}
}

// A fleet request goes through the same signature check as every other
// message: nothing is read from an unsigned, wrongly signed or tampered body,
// and a rotated-out secret is accepted exactly as for other messages.
func TestFleetRequestSignatureIsChecked(t *testing.T) {
	const oldSecret = "old-secret"
	msg := fleetBody(t, goodFleetRequest())
	tampered := fleetBody(t, &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{fleetOID1}, Data: limacharlie.Dict{}})
	for name, gzipped := range encodings {
		for _, tc := range []struct {
			name     string
			sig      string
			body     []byte
			previous []string
			want     int
			calls    int
		}{
			{"valid", sign(msg), msg, nil, http.StatusOK, 1},
			{"previous secret", signWith(oldSecret, msg), msg, []string{oldSecret}, http.StatusOK, 1},
			{"wrong secret", signWith("other-secret", msg), msg, []string{oldSecret}, http.StatusUnauthorized, 0},
			{"previous secret once removed", signWith(oldSecret, msg), msg, nil, http.StatusUnauthorized, 0},
			{"signature of another body", sign(msg), tampered, nil, http.StatusUnauthorized, 0},
			{"missing signature", "", msg, nil, http.StatusOK, 0},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				fc := &fleetCall{}
				ext := newFleetExtension(t, fc)
				ext.PreviousSecretKeys = tc.previous
				body := tc.body
				if gzipped {
					body = gz(t, tc.body)
				}
				rec := post(ext, body, tc.sig, gzipped)
				if rec.Code != tc.want || fc.calls != tc.calls {
					t.Fatalf("status %d calls %d, want %d and %d: %s", rec.Code, fc.calls, tc.want, tc.calls, rec.Body.String())
				}
				if tc.calls == 0 && rec.Body.Len() != 0 {
					t.Fatalf("an unauthenticated sender got an answer: %s", rec.Body.String())
				}
			})
		}
	}
}

// The SDK refuses what the platform never sends, without calling the handler.
func TestFleetRequestRefusals(t *testing.T) {
	tooMany := make([]string, common.MaxFleetRequestOIDs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("0f2c8d7e-1111-4a2b-9c3d-%012d", i)
	}
	atMost := tooMany[:common.MaxFleetRequestOIDs]
	for _, tc := range []struct {
		name    string
		fr      *common.FleetRequestMessage
		want    int
		errPart string
	}{
		{"unknown action", &common.FleetRequestMessage{Action: "nope", OIDs: []string{fleetOID1}}, 400, "unknown fleet action: nope"},
		{"per-organization action", &common.FleetRequestMessage{Action: testAction, OIDs: []string{fleetOID1}}, 400, "unknown fleet action"},
		{"no oids", &common.FleetRequestMessage{Action: fleetAction}, 400, "no organization"},
		{"empty oids", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{}}, 400, "no organization"},
		{"too many oids", &common.FleetRequestMessage{Action: fleetAction, OIDs: tooMany}, 400, "the most is 1000"},
		{"exactly the most", &common.FleetRequestMessage{Action: fleetAction, OIDs: atMost}, 200, ""},
		{"not a uuid", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{fleetOID1, "oid-1"}}, 400, "organization 1 is not a lowercase UUID"},
		{"uppercase uuid", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{strings.ToUpper(fleetOID1)}}, 400, "not a lowercase UUID"},
		{"uuid with a suffix", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{fleetOID1 + "0"}}, 400, "not a lowercase UUID"},
		{"empty oid", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{""}}, 400, "not a lowercase UUID"},
		{"data of the wrong shape", &common.FleetRequestMessage{Action: fleetAction, OIDs: []string{fleetOID1}, Data: limacharlie.Dict{"rows": "yes"}}, 400, "failed to unmarshal request data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fleetCall{}
			ext := newFleetExtension(t, fc)
			msg := fleetBody(t, tc.fr)
			rec := post(ext, msg, sign(msg), false)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 200 {
				if fc.calls != 1 {
					t.Fatalf("handler calls %d", fc.calls)
				}
				return
			}
			if fc.calls != 0 {
				t.Fatalf("handler ran for a refused request")
			}
			var resp common.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(resp.Error, tc.errPart) {
				t.Fatalf("error %q, want it to contain %q", resp.Error, tc.errPart)
			}
		})
	}
}

// A fleet section next to any other section is refused whole.
func TestFleetRequestMustBeAlone(t *testing.T) {
	for name, other := range map[string]common.Message{
		"request":    {Request: &common.RequestMessage{Org: common.OrgAccessData{OID: "o"}, Action: testAction}},
		"event":      {Event: &common.EventMessage{Org: common.OrgAccessData{OID: "o"}}},
		"error":      {ErrorReport: &common.ErrorReportMessage{Error: "e"}},
		"validation": {ConfigValidation: &common.ConfigValidationMessage{}},
		"schema":     {SchemaRequest: &common.SchemaRequestMessage{}},
	} {
		t.Run(name, func(t *testing.T) {
			fc := &fleetCall{}
			ext := newFleetExtension(t, fc)
			m := other
			m.Version = PROTOCOL_VERSION
			m.FleetRequest = goodFleetRequest()
			msg, err := json.Marshal(&m)
			if err != nil {
				t.Fatal(err)
			}
			rec := post(ext, msg, sign(msg), false)
			if rec.Code != http.StatusBadRequest || fc.calls != 0 {
				t.Fatalf("status %d calls %d: %s", rec.Code, fc.calls, rec.Body.String())
			}
		})
	}
}

// An Extension that did not opt in never receives a fleet request: no handler,
// or a handler for an action its schema does not declare fleet, and the
// per-organization path never reaches a fleet handler either.
func TestFleetRequestNeedsTheOptIn(t *testing.T) {
	msg := fleetBody(t, goodFleetRequest())

	t.Run("no fleet handlers at all", func(t *testing.T) {
		calls := 0
		ext, done := newPingExtension(t, &calls)
		defer done()
		rec := post(ext, msg, sign(msg), false)
		if rec.Code != http.StatusBadRequest || calls != 0 {
			t.Fatalf("status %d calls %d: %s", rec.Code, calls, rec.Body.String())
		}
	})

	t.Run("handler registered but action not declared fleet", func(t *testing.T) {
		// Init refuses this; a caller that skipped Init still gets no dispatch.
		fc := &fleetCall{}
		ext := newFleetExtension(t, fc)
		ext.RequestSchema[fleetAction] = common.RequestSchema{ShortDescription: "fleet"}
		if rec := post(ext, msg, sign(msg), false); rec.Code != http.StatusBadRequest || fc.calls != 0 {
			t.Fatalf("status %d calls %d: %s", rec.Code, fc.calls, rec.Body.String())
		}
		ext.RequestSchema = nil
		if rec := post(ext, msg, sign(msg), false); rec.Code != http.StatusBadRequest || fc.calls != 0 {
			t.Fatalf("status %d calls %d: %s", rec.Code, fc.calls, rec.Body.String())
		}
	})

	t.Run("per-organization request for a fleet action", func(t *testing.T) {
		fc := &fleetCall{}
		ext := newFleetExtension(t, fc)
		ms := limacharlie.NewMockServer("oid-test")
		defer ms.Close()
		org, err := ms.NewOrganization()
		if err != nil {
			t.Fatal(err)
		}
		ext.OrgFromAccess = func(common.OrgAccessData) (*limacharlie.Organization, error) { return org, nil }
		req, err := json.Marshal(&common.Message{Version: PROTOCOL_VERSION, Request: &common.RequestMessage{
			Org: common.OrgAccessData{OID: "o"}, Action: fleetAction,
		}})
		if err != nil {
			t.Fatal(err)
		}
		rec := post(ext, req, sign(req), false)
		if rec.Code != http.StatusBadRequest || fc.calls != 0 || !strings.Contains(rec.Body.String(), "unknown request action") {
			t.Fatalf("status %d calls %d: %s", rec.Code, fc.calls, rec.Body.String())
		}
	})
}

func TestInitChecksFleetDeclarations(t *testing.T) {
	handler := FleetRequestCallback{Callback: func(context.Context, FleetRequestCallbackParams) common.Response {
		return common.Response{}
	}}
	request := RequestCallback{Callback: func(context.Context, RequestCallbackParams) common.Response {
		return common.Response{}
	}}
	for _, tc := range []struct {
		name    string
		schema  common.RequestSchemas
		cb      ExtensionCallbacks
		errPart string
	}{
		{"nothing fleet", common.RequestSchemas{"a": {}}, ExtensionCallbacks{RequestHandlers: map[common.ActionName]RequestCallback{"a": request}}, ""},
		{"declared and handled", common.RequestSchemas{"f": {IsFleet: true}}, ExtensionCallbacks{FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{"f": handler}}, ""},
		{"handler without a declaration", common.RequestSchemas{"f": {}}, ExtensionCallbacks{FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{"f": handler}}, `fleet handler "f" is not declared fleet`},
		{"handler for an action with no schema", nil, ExtensionCallbacks{FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{"f": handler}}, `fleet handler "f" is not declared fleet`},
		{"declaration without a handler", common.RequestSchemas{"f": {IsFleet: true}}, ExtensionCallbacks{}, `action "f" is declared fleet in RequestSchema but has no fleet handler`},
		{"declared with a request handler only", common.RequestSchemas{"f": {IsFleet: true}}, ExtensionCallbacks{RequestHandlers: map[common.ActionName]RequestCallback{"f": request}}, "has no fleet handler"},
		{"both handlers", common.RequestSchemas{"f": {IsFleet: true}}, ExtensionCallbacks{
			FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{"f": handler},
			RequestHandlers:      map[common.ActionName]RequestCallback{"f": request},
		}, `action "f" has both a fleet handler and a request handler`},
		{"handler with no callback", common.RequestSchemas{"f": {IsFleet: true}}, ExtensionCallbacks{FleetRequestHandlers: map[common.ActionName]FleetRequestCallback{"f": {}}}, `fleet handler "f" has no callback`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := &Extension{SecretKey: testSecret, RequestSchema: tc.schema, Callbacks: tc.cb}
			err := ext.Init()
			if tc.errPart == "" {
				if err != nil {
					t.Fatalf("Init: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("Init error %v, want it to contain %q", err, tc.errPart)
			}
		})
	}
}

// A handler's error is relayed the way a request handler's is: retriable
// (the default) is a 500 the platform retries or reports, a non-retriable one
// a 400.
func TestFleetHandlerErrors(t *testing.T) {
	no := false
	for _, tc := range []struct {
		name string
		resp common.Response
		want int
	}{
		{"retriable by default", common.Response{Error: "store unavailable"}, http.StatusInternalServerError},
		{"non retriable", common.Response{Error: "bad_request_type", Retriable: &no}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fleetCall{}
			ext := newFleetExtension(t, fc)
			ext.Callbacks.FleetRequestHandlers[fleetAction] = FleetRequestCallback{
				Callback: func(context.Context, FleetRequestCallbackParams) common.Response { return tc.resp },
			}
			msg := fleetBody(t, goodFleetRequest())
			rec := post(ext, msg, sign(msg), false)
			var resp common.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.want || resp.Error != tc.resp.Error {
				t.Fatalf("status %d error %q, want %d and %q", rec.Code, resp.Error, tc.want, tc.resp.Error)
			}
		})
	}
}

// The per-organization path is unchanged next to a fleet handler.
func TestRequestStillWorksNextToFleetHandler(t *testing.T) {
	fc := &fleetCall{}
	ext := newFleetExtension(t, fc)
	ms := limacharlie.NewMockServer("oid-test")
	defer ms.Close()
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatal(err)
	}
	ext.OrgFromAccess = func(common.OrgAccessData) (*limacharlie.Organization, error) { return org, nil }
	msg := pingMessage(t)
	if rec := post(ext, msg, sign(msg), false); rec.Code != http.StatusOK || fc.calls != 0 {
		t.Fatalf("status %d fleet calls %d: %s", rec.Code, fc.calls, rec.Body.String())
	}
}
