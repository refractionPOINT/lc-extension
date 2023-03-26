package common

// A data object key.
type DataKey = string

// This is a config object. It can be a root of an
// Extension's configs, or it can be a sub object.
type ConfigObjectSchema struct {
	Fields map[DataKey]DataElement `json:"fields" msgpack:"fields"`
	// All field sets must be satisfied.
	// Each field is specifies fields where one and only one must be set.
	Requirements []RequiredFields `json:"requirements" msgpack:"requirements"`
}

// Valid objects require one of the following fields to be specified.
type RequiredFields = []DataKey

type DataElement struct {
	Description  string            `json:"description" msgpack:"description"`
	DataType     ParameterDataType `json:"data_type" msgpack:"data_type"`
	IsList       bool              `json:"is_list,omitempty" msgpack:"is_list,omitempty"` // Is this Parameter for a single item, or a list of items?
	DisplayIndex int               `json:"display_index,omitempty" msgpack:"display_index,omitempty"`

	// If this element is an Object, this field
	// will contain the definition of this Object.
	Object *ConfigObjectSchema `json:"object,omitempty" msgpack:"object,omitempty"`

	// This Element is populated when IsList is True.
	Elements *DataElement `json:"elements,omitempty" msgpack:"elements,omitempty"`
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
