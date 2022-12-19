package common

import "github.com/refractionPOINT/go-limacharlie/limacharlie"

type Message struct {
	// Header always specified.
	Version        uint64 `json:"version"`
	IdempotencyKey string `json:"idempotency_key"`

	// One of the following will be specified.
	HeartBeat *HeartBeatMessage `json:"heartbeat,omitempty"`
	Request   *RequestMessage   `json:"request,omitempty"`
	Event     *EventMessage     `json:"event,omitempty"`
}

type HeartBeatMessage struct{}

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
	Error   string      `json:"error"`
	Version uint64      `json:"version"`
	Data    interface{} `json:"data,omitempty"`
}

var EventTypes = struct {
	Subscribe   string
	Unsubscribe string
}{
	Subscribe:   "subscribe",
	Unsubscribe: "unsubscribe",
}