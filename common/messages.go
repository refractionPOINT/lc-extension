package common

type Message struct {
	// Header always specified.
	Version        uint64 `json:"version" msgpack:"version"`
	IdempotencyKey string `json:"idempotency_key" msgpack:"idempotency_key"`

	// One of the following will be specified.
	HeartBeat        *HeartBeatMessage        `json:"heartbeat,omitempty" msgpack:"heartbeat,omitempty"`
	ErrorReport      *ErrorReportMessage      `json:"error_report,omitempty" msgpack:"error_report,omitempty"`
	ConfigValidation *ConfigValidationMessage `json:"conf_validation,omitempty" msgpack:"conf_validation,omitempty"`
	SchemaRequest    *SchemaRequestMessage    `json:"schema_request,omitempty" msgpack:"schema_request,omitempty"`
	Request          *RequestMessage          `json:"request,omitempty" msgpack:"request,omitempty"`
	Event            *EventMessage            `json:"event,omitempty" msgpack:"event,omitempty"`
}

type HeartBeatMessage struct{}
type HeartBeatResponse struct{}

type ErrorReportMessage struct {
	Error string `json:"error" msgpack:"error"`
	Oid   string `json:"oid,omitempty" msgpack:"oid,omitempty"`
}

type ConfigValidationMessage struct {
	Org    OrgAccessData          `json:"org" msgpack:"org"`
	Config map[string]interface{} `json:"conf" msgpack:"conf"`
}

type ConfigValidationResponse struct{}

type SchemaRequestMessage struct{}
type SchemaRequestResponse struct {
	Config         ConfigObjectSchema `json:"config_schema" msgpack:"config_schema"`
	Request        RequestSchemas     `json:"request_schema" msgpack:"request_schema"`
	RequiredEvents []EventName        `json:"required_events" msgpack:"required_events"`
}

type OrgAccessData struct {
	OID string `json:"oid"`
	JWT string `json:"jwt"`
}

type ActionName = string

type RequestMessage struct {
	Org    OrgAccessData          `json:"org" msgpack:"org"`
	Action ActionName             `json:"action" msgpack:"action"`
	Data   map[string]interface{} `json:"data" msgpack:"data"`
	Config map[string]interface{} `json:"config" msgpack:"config"`
}

type ContinuationRequest struct {
	InDelaySeconds uint64                 `json:"in_delay_sec" msgpack:"in_delay_sec"`
	Action         ActionName             `json:"action" msgpack:"action"`
	State          map[string]interface{} `json:"state" msgpack:"state"`
}

type EventMessage struct {
	Org       OrgAccessData          `json:"org" msgpack:"org"`
	EventName EventName              `json:"event_name" msgpack:"event_name"`
	Data      map[string]interface{} `json:"data" msgpack:"data"`
	Config    map[string]interface{} `json:"config" msgpack:"config"`
}

type Response struct {
	Error             string                `json:"error" msgpack:"error"`
	Version           uint64                `json:"version" msgpack:"version"`
	Data              interface{}           `json:"data,omitempty" msgpack:"data,omitempty"`
	SensorStateChange *SensorUpdate         `json:"ssc,omitempty" msgpack:"ssc,omitempty"` // For internal use only.
	Continuations     []ContinuationRequest `json:"continuations,omitempty" msgpack:"continuations,omitempty"`
}

type EventName = string

// For internal use only.
type SensorUpdate struct {
	SID         string                 `json:"sid" msgpack:"sid"`
	CollectorID string                 `json:"col_id" msgpack:"col_id"`
	UpdateTS    uint64                 `json:"update_ts" msgpack:"update_ts"`
	Data        map[string]interface{} `json:"data" msgpack:"data"`
}

var EventTypes = struct {
	Subscribe   EventName
	Unsubscribe EventName
}{
	Subscribe:   "subscribe",
	Unsubscribe: "unsubscribe",
}
