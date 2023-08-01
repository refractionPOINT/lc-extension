package common

type Metric struct {
	Sku   string `json:"sku" msgpack:"sku"`
	Value uint64 `json:"value" msgpack:"value"`
}

type MetricReport struct {
	IdempotentKey string   `json:"idempotent_key" msgpack:"idempotent_key"`
	Metrics       []Metric `json:"metrics" msgpack:"metrics"`
}
