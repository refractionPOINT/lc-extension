package common

import "github.com/refractionPOINT/go-limacharlie/limacharlie"

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
	Org    OrgAccessData    `json:"org"`
	Action ActionName       `json:"action"`
	Data   limacharlie.Dict `json:"data"`
}

type EventMessage struct {
	Org       OrgAccessData `json:"org"`
	EventName string        `json:"event_name"`
	Data      interface{}   `json:"data"`
}

type Response struct {
	Error             string        `json:"error"`
	Version           uint64        `json:"version"`
	Data              interface{}   `json:"data,omitempty"`
	SensorStateChange *SensorUpdate `json:"ssc,omitempty"` // For internal use only.
}

// For internal use only.
type SensorUpdate struct {
	SID         string                 `json:"sid" msgpack:"sid"`
	CollectorID string                 `json:"col_id" msgpack:"col_id"`
	UpdateTS    uint64                 `json:"update_ts" msgpack:"update_ts"`
	Data        map[string]interface{} `json:"data" msgpack:"data"`
}

var EventTypes = struct {
	Subscribe   string
	Unsubscribe string
}{
	Subscribe:   "subscribe",
	Unsubscribe: "unsubscribe",
}
