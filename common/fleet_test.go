package common

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// The wire keys of a fleet request are what the platform sends; a renamed tag
// would silently drop the request's organizations.
func TestFleetRequestWireShape(t *testing.T) {
	body := `{"version":20221218,"idempotency_key":"k1","fleet_request":{"action":"summary",` +
		`"oids":["0f2c8d7e-1111-4a2b-9c3d-000000000001","0f2c8d7e-1111-4a2b-9c3d-000000000002"],` +
		`"data":{"rows":true},"ident":"user@example.com"}}`
	var m Message
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m.Request != nil || m.Event != nil {
		t.Fatalf("a fleet request is not a per-org request: %+v", m)
	}
	want := &FleetRequestMessage{
		Action: "summary",
		OIDs:   []string{"0f2c8d7e-1111-4a2b-9c3d-000000000001", "0f2c8d7e-1111-4a2b-9c3d-000000000002"},
		Data:   map[string]interface{}{"rows": true},
		Ident:  "user@example.com",
	}
	if !reflect.DeepEqual(m.FleetRequest, want) {
		t.Fatalf("decoded %+v, want %+v", m.FleetRequest, want)
	}

	packed, err := msgpack.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Message
	if err := msgpack.Unmarshal(packed, &back); err != nil {
		t.Fatal(err)
	}
	if back.FleetRequest == nil || !reflect.DeepEqual(back.FleetRequest.OIDs, want.OIDs) || back.FleetRequest.Action != want.Action {
		t.Fatalf("msgpack round trip %+v", back.FleetRequest)
	}
}

// An action that does not opt in publishes exactly the schema it published
// before the flag existed, so older platform components see no change.
func TestFleetFlagOnlyWhenSet(t *testing.T) {
	plain, err := json.Marshal(RequestSchema{ShortDescription: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"fleet"`) {
		t.Fatalf("an action that is not a fleet action carries the flag: %s", plain)
	}
	fleet, err := json.Marshal(RequestSchema{ShortDescription: "s", IsFleet: true})
	if err != nil {
		t.Fatal(err)
	}
	var back RequestSchema
	if err := json.Unmarshal(fleet, &back); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fleet), `"fleet":true`) || !back.IsFleet {
		t.Fatalf("fleet flag not on the wire: %s", fleet)
	}
}
