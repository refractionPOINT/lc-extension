package common

// A data object key.
type SchemaKey = string

// This is a config object. It can be a root of an
// Extension's configs, or it can be a sub object.
type SchemaObject struct {
	Fields map[SchemaKey]SchemaElement `json:"fields" msgpack:"fields"`
	// for type "Record" and lists of "Object"
	Key                RecordKey `json:"key,omitempty" msgpack:"key,omitempty"`
	ElementName        string    `json:"element_name,omitempty" msgpack:"element_name,omitempty"`
	ElementDescription string    `json:"element_desc,omitempty" msgpack:"element_desc,omitempty"`

	// Extended definition for Interactive elements
	// --------------------------------------------

	// All field sets must be satisfied.
	// Each field is specifies fields where one and only one must be set.
	Requirements []RequiredFields `json:"requirements" msgpack:"requirements"`

	// List of Requests that can be performed on the response data.
	// will translate into buttons that issue a follow-up Request with the response data
	SupportedActions []ActionName `json:"supported_actions,omitempty" msgpack:"supported_actions,omitempty"`
}

type RecordKey = struct {
	Name         string         `json:"name,omitempty" msgpack:"name,omitempty"`
	Label        Label          `json:"label,omitempty" msgpack:"label,omitempty"`
	Description  string         `json:"description,omitempty" msgpack:"description,omitempty"`
	DataType     SchemaDataType `json:"data_type,omitempty" msgpack:"data_type,omitempty"`
	DisplayIndex int            `json:"display_index,omitempty" msgpack:"display_index,omitempty"`
	PlaceHolder  string         `json:"placeholder,omitempty" msgpack:"placeholder,omitempty"`
}

// Valid objects require one of the following fields to be specified.
type RequiredFields = []SchemaKey

type Validator = struct {
	// for events, string types (mutually exclusive)
	WhiteList []string `json:"whitelist,omitempty" msgpack:"whitelist,omitempty"`
	Blacklist []string `json:"blacklist,omitempty" msgpack:"blacklist,omitempty"`
	// for string types (mutually exclusive)
	ValidRE   string `json:"valid_re,omitempty" msgpack:"valid_re,omitempty"`
	InvalidRE string `json:"invalid_re,omitempty" msgpack:"invalid_re,omitempty"`
	// for number and time/date data_types
	Min int `json:"min,omitempty" msgpack:"min,omitempty"`
	Max int `json:"max,omitempty" msgpack:"max,omitempty"`
	// for platform and sid types
	Platforms []string `json:"platforms,omitempty" msgpack:"platforms,omitempty"`
}

type ComplexEnumValues = struct {
	Label         string `json:"label" msgpack:"label"`
	Value         string `json:"value" msgpack:"value"`
	CategoryKey   string `json:"category_key,omitempty" msgpack:"category_key,omitempty"`     // allows for categories to be selected in bulk
	ReferenceLink string `json:"reference_link,omitempty" msgpack:"reference_link,omitempty"` // documentation
}

type SchemaElement struct {
	Label        Label          `json:"label,omitempty" msgpack:"label,omitempty"` // Human readable label.
	Description  string         `json:"description" msgpack:"description"`
	PlaceHolder  string         `json:"placeholder,omitempty" msgpack:"placeholder,omitempty"` // Placeholder to display for this field.
	DataType     SchemaDataType `json:"data_type" msgpack:"data_type"`
	IsList       bool           `json:"is_list,omitempty" msgpack:"is_list,omitempty"` // Is this Parameter for a single item, or a list of items?
	DisplayIndex int            `json:"display_index,omitempty" msgpack:"display_index,omitempty"`
	DefaultValue interface{}    `json:"default_value,omitempty" msgpack:"default_value,omitempty"` // If a default value should be set for is_required: false Parameters.

	// If this element is an Object, this field
	// will contain the definition of this Object.
	Object *SchemaObject `json:"object,omitempty" msgpack:"object,omitempty"`

	// Extended definition for Interactive elements
	// -------------------------------------------
	EnumValues        []interface{}       `json:"enum_values,omitempty" msgpack:"enum_values,omitempty"` // If the type is enum, these are the possible values.
	ComplexEnumValues []ComplexEnumValues `json:"complex_enum_values,omitempty" msgpack:"complex_enum_values,omitempty"`
	Filter            Validator           `json:"filter,omitempty" msgpack:"filter,omitempty"`
}

// Example of a config for something like a Sigma Extension.
// {
// 	"enable_new_rules": {
// 		"description": "if set to true, will automatically enable new Sigma rules",
// 		"is_required": false,
// 		"data_type": "bool",
// 		"display_index": 0
// 	},
// 	"suppression": {
// 		"description": "suppression configurations",
// 		"is_required": false,
// 		"data_type": "object",
// 		"display_index": 1,
// 		"object": {
// 			"suppression_time": {
// 				"description": "if set, will suppress detections per sensor per rule for this duration",
// 				"is_required": false,
// 				"data_type": "duration",
// 				"display_index": 0
// 			}
// 		}
// 	}
// }
//
// Which could look like this actual config.
// {
// 	"enable_new_rules": true,
// 	"suppression": {
// 		"suppression_time": 10800000,
// 	}
// }
