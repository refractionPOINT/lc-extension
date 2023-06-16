package common

// A data object key.
type SchemaKey = string

// This is a config object. It can be a root of an
// Extension's configs, or it can be a sub object.
type SchemaObject struct {
	Fields map[SchemaKey]SchemaElement `json:"fields" msgpack:"fields"`

	// Extended definition for Response elements.
	// Not available at the root of the Response.
	// -------------------------------------------
	RenderType      string         `json:"render_type,omitempty" msgpack:"render_type,omitempty"`
	KeyDataType     SchemaDataType `json:"key_data_type,omitempty" msgpack:"key_data_type,omitempty"`
	KeyName         string         `json:"key_name,omitempty" msgpack:"key_name,omitempty"`
<<<<<<< HEAD
	KeyLabel        string         `json:"key_label,omitempty" msgpack:"key_label,omitempty"`
	KeyDisplayIndex int            `json:"key_display_index,omitempty" msgpack:"key_display_index,omitempty"`
=======
	KeyLabel        string         `json:"key_label,omitempty" msgpack:"key_name,omitempty"`
	KeyDisplayIndex int            `json:"key_display_index,omitempty" msgpack:"display_index,omitempty"`
>>>>>>> e7ec2a24bc9904b83339998bb702d04238d65424

	// Extended definition for Interactive elements
	// like Configs and Requests.
	// -------------------------------------------
	// All field sets must be satisfied.
	// Each field is specifies fields where one and only one must be set.
	Requirements []RequiredFields `json:"requirements" msgpack:"requirements"`
}

// Valid objects require one of the following fields to be specified.
type RequiredFields = []SchemaKey

type SchemaElement struct {
	Label        Label          `json:"label,omitempty" msgpack:"label,omitempty"` // Human readable label.
	Description  string         `json:"description" msgpack:"description"`
	DataType     SchemaDataType `json:"data_type" msgpack:"data_type"`
	IsList       bool           `json:"is_list,omitempty" msgpack:"is_list,omitempty"` // Is this Parameter for a single item, or a list of items?
	DisplayIndex int            `json:"display_index,omitempty" msgpack:"display_index,omitempty"`
	DefaultValue interface{}    `json:"default_value,omitempty" msgpack:"default_value,omitempty"` // If a default value should be set for is_required: false Parameters.

	// If this element is an Object, this field
	// will contain the definition of this Object.
	Object *SchemaObject `json:"object,omitempty" msgpack:"object,omitempty"`

	// Extended definition for Interactive elements
	// like Configs and Requests.
	// -------------------------------------------
	EnumValues  []interface{} `json:"enum_values,omitempty" msgpack:"enum_values,omitempty"` // If the type is enum, these are the possible values.
	PlaceHolder string        `json:"placeholder" msgpack:"placeholder"`                     // Placeholder to display for this field.

	// Extended definition for Actionable elements
	// like Configs and Responses.
	// -------------------------------------------
	// List of Requests that can be performed on the given
	// element. Will translate into buttons on elements that
	// will issue a Request to Extension with the element's
	// data included.
	SupportedActions []ActionName `json:"supported_actions,omitempty" msgpack:"supported_actions,omitempty"`
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
// 		"is_requried": false,
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
