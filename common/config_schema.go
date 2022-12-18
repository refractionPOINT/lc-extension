package common

// A config object key.
type ConfigKey = string

// This is a config object. It can be a root of an
// Extension's configs, or it can be a sub object.
type ConfigObjectSchema = map[ConfigKey]ConfigElement

type ConfigElement struct {
	IsRequired   bool              `json:"is_required"`
	DataType     ParameterDataType `json:"data_type"`
	DisplayIndex int               `json:"display_index,omitempty"`

	// If this element is an Object, this field
	// will contain the definition of this Object.
	Object ConfigObjectSchema `json:"object,omitempty"`
}
