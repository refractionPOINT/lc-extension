package common

// A config object key.
type ConfigKey = string

// This is a config object. It can be a root of an
// Extension's configs, or it can be a sub object.
type ConfigObjectSchema = map[ConfigKey]ConfigElement

type ConfigElement struct {
	Description  string            `json:"description"`
	IsRequired   bool              `json:"is_required"`
	DataType     ParameterDataType `json:"data_type"`
	IsList       bool              `json:"is_list,omitempty"` // Is this Parameter for a single item, or a list of items?
	DisplayIndex int               `json:"display_index,omitempty"`

	// If this element is an Object, this field
	// will contain the definition of this Object.
	Object ConfigObjectSchema `json:"object,omitempty"`

	// This Element is populated when IsList is True.
	Elements *ConfigElement `json:"elements,omitempty"`
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
// 		"suppression_time": "3h",
// 	}
// }
