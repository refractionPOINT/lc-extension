package common

type Message struct {
	// Header always specified.
	Version        uint64 `json:"version"`
	IdempotencyKey string `json:"idempotency_key"`

	// One of the following will be specified.
	HeartBeat        *HeartBeatMessage        `json:"heartbeat,omitempty"`
	ConfigValidation *ConfigValidationMessage `json:"conf_validation,omitempty"`
	Request          *RequestMessage          `json:"request,omitempty"`
	Event            *EventMessage            `json:"event,omitempty"`
}

type HeartBeatMessage struct{}
type HeartBeatResponse struct{}

type ConfigValidationMessage struct {
	Org    OrgAccessData          `json:"org"`
	Config map[string]interface{} `json:"conf"`
}

type ConfigValidationResponse struct{}

type OrgAccessData struct {
	OID string `json:"oid"`
	JWT string `json:"jwt"`
}

type ActionName = string

type RequestMessage struct {
	Org    OrgAccessData          `json:"org"`
	Action ActionName             `json:"action"`
	Data   map[string]interface{} `json:"data"`
	Config map[string]interface{} `json:"config"`
}

type ContinuationRequest struct {
	InDelaySeconds uint64                 `json:"in_delay_sec"`
	Action         ActionName             `json:"action"`
	State          map[string]interface{} `json:"state"`
}

type EventMessage struct {
	Org       OrgAccessData          `json:"org"`
	EventName EventName              `json:"event_name"`
	Data      map[string]interface{} `json:"data"`
	Config    map[string]interface{} `json:"config"`
}

type Response struct {
	Error             string                `json:"error"`
	Version           uint64                `json:"version"`
	Data              interface{}           `json:"data,omitempty"`
	SensorStateChange *SensorUpdate         `json:"ssc,omitempty"` // For internal use only.
	Continuations     []ContinuationRequest `json:"continuations,omitempty"`
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
	Every1Hour  EventName
	Every3Hour  EventName
	Every12Hour EventName
	Every24Hour EventName
	Every7Day   EventName
	Every30Day  EventName
}{
	Subscribe:   "subscribe",
	Unsubscribe: "unsubscribe",
	Every1Hour:  "every_1h",
	Every3Hour:  "every_3h",
	Every12Hour: "every_12h",
	Every24Hour: "every_24h",
	Every7Day:   "every_7d",
	Every30Day:  "every_30d",
}
