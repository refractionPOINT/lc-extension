package core

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

// FleetRequestCallbackParams is what a fleet handler receives. It deliberately
// has no *limacharlie.Organization, credentials, configuration or ACL block:
// a fleet request is about several organizations and carries none of them. The
// handler answers from the Extension's own store and must never call
// LimaCharlie on behalf of the organizations it is given.
type FleetRequestCallbackParams struct {
	// OIDs are the organizations the read covers. LimaCharlie has already
	// decided that the asker may see all of them, so the list is authoritative.
	// The SDK has checked that there is at least one and at most
	// common.MaxFleetRequestOIDs, that each is a lowercase UUID, and that the
	// list is strictly ascending (so without duplicates).
	OIDs []string
	// Request is the decoded action parameters (a *RequestStruct when the
	// callback declares one, a limacharlie.Dict otherwise).
	Request interface{}
	// Ident says who asked, for the audit log only. It is never an
	// authorization input.
	Ident         string
	IdempotentKey string
}

// FleetRequestCallback answers one fleet action. RequestStruct works exactly as
// it does for RequestCallback.
type FleetRequestCallback struct {
	RequestStruct interface{}
	Callback      func(ctx context.Context, params FleetRequestCallbackParams) common.Response
}

// validateFleetHandlers is the opt-in contract between the schema an Extension
// publishes and the handlers it registers: a fleet handler needs an action
// declared IsFleet, and an IsFleet action needs a fleet handler and no
// per-organization one. LimaCharlie decides what to send from the schema, so
// the two must agree.
func (e *Extension) validateFleetHandlers() error {
	problems := []string{}
	for action, cb := range e.Callbacks.FleetRequestHandlers {
		if cb.Callback == nil {
			problems = append(problems, fmt.Sprintf("fleet handler %q has no callback", action))
		}
		if schema, ok := e.RequestSchema[action]; !ok || !schema.IsFleet {
			problems = append(problems, fmt.Sprintf("fleet handler %q is not declared fleet in RequestSchema", action))
		}
		if _, ok := e.Callbacks.RequestHandlers[action]; ok {
			problems = append(problems, fmt.Sprintf("action %q has both a fleet handler and a request handler", action))
		}
	}
	for action, schema := range e.RequestSchema {
		if !schema.IsFleet {
			continue
		}
		if _, ok := e.Callbacks.FleetRequestHandlers[action]; !ok {
			problems = append(problems, fmt.Sprintf("action %q is declared fleet in RequestSchema but has no fleet handler", action))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid fleet configuration: %s", strings.Join(problems, "; "))
}

// handleFleetRequest answers a fleet request that has already passed the
// signature check, like every other message. A non-zero status means the
// request was refused before any handler ran.
func (e *Extension) handleFleetRequest(ctx context.Context, message *common.Message) (common.Response, int) {
	response := common.Response{Version: PROTOCOL_VERSION}
	fr := message.FleetRequest

	refuse := func(format string, args ...interface{}) (common.Response, int) {
		response.Error = fmt.Sprintf(format, args...)
		if e.Callbacks.ErrorHandler != nil {
			e.Callbacks.ErrorHandler(&common.ErrorReportMessage{Error: response.Error})
		}
		return response, http.StatusBadRequest
	}

	// The fleet section is the whole message: a crafted envelope that also
	// carries a per-organization section is never half handled.
	if message.HeartBeat != nil || message.Request != nil || message.Event != nil || message.ErrorReport != nil ||
		message.ConfigValidation != nil || message.SchemaRequest != nil {
		return refuse("a fleet request must be the only section of its message")
	}

	// Only an action the Extension declared fleet is ever answered, and only
	// with a fleet handler: an Extension that did not opt in never receives
	// this message's content.
	if schema, ok := e.RequestSchema[fr.Action]; !ok || !schema.IsFleet {
		return refuse("unknown fleet action: %s", fr.Action)
	}
	cb, ok := e.Callbacks.FleetRequestHandlers[fr.Action]
	if !ok || cb.Callback == nil {
		return refuse("unknown fleet action: %s", fr.Action)
	}
	if len(fr.OIDs) == 0 {
		return refuse("fleet request names no organization")
	}
	if len(fr.OIDs) > common.MaxFleetRequestOIDs {
		return refuse("fleet request names %d organizations, the most is %d", len(fr.OIDs), common.MaxFleetRequestOIDs)
	}
	for i, oid := range fr.OIDs {
		if !isCanonicalUUID(oid) {
			return refuse("fleet request organization %d is not a lowercase UUID", i)
		}
		// Sorted ascending without duplicates, as the message documents: a
		// handler may merge or count on it.
		if i > 0 && oid <= fr.OIDs[i-1] {
			return refuse("fleet request organization %d is a duplicate or out of order", i)
		}
	}

	data := fr.Data
	if data == nil {
		data = limacharlie.Dict{}
	}
	var request interface{} = data
	if cb.RequestStruct != nil && !(reflect.ValueOf(cb.RequestStruct).Kind() == reflect.Pointer && reflect.ValueOf(cb.RequestStruct).IsNil()) {
		var err error
		request, err = unmarshalToStruct(data, cb.RequestStruct)
		if err != nil {
			response.Error = fmt.Sprintf("failed to unmarshal request data: %v", err)
			return response, http.StatusBadRequest
		}
	}

	return cb.Callback(ctx, FleetRequestCallbackParams{
		OIDs:          fr.OIDs,
		Request:       request,
		Ident:         fr.Ident,
		IdempotentKey: message.IdempotencyKey,
	}), 0
}

// isCanonicalUUID reports whether s is a UUID in its canonical lowercase form
// (8-4-4-4-12 hexadecimal digits).
func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}
