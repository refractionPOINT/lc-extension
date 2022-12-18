package common

// An Action name to ask the Extension to perform.
type RequestAction = string

// List of Parameters expected per Action.
type RequestSchemas = map[RequestAction]RequestSchema

// Shema of expected Parameters for a specific request Action.
type RequestSchema struct {
	IsUserFacing     bool                        `json:"is_user_facing"`    // Is this Action expected to be performed by a human, or is it for automation.
	ShortDescription string                      `json:"short_description"` // Short description of what this Action does.
	LongDescription  string                      `json:"long_description"`  // Longer version of the Short Description.
	Parameters       RequestParameterDefinitions `json:"parameters"`        // List of Parameter Names and their definition.
}

// A Parameter Name.
type RequestParameterName = string

// List of Parameters Definition per Parameter Name.
type RequestParameterDefinitions = map[RequestParameterName]RequestParameterDefinition

// The Definition of a Parameter.
type RequestParameterDefinition struct {
	IsRequired   bool              `json:"is_required"`             // Is this Parameter required for this Action?
	IsList       bool              `json:"is_list,omitempty"`       // Is this Parameter for a single item, or a list of items?
	DataType     ParameterDataType `json:"data_type"`               // The type of values expected.
	DefaultValue string            `json:"default_value,omitempty"` // If a default value should be set for is_required: false Parameters.

	DisplayIndex int `json:"display_index,omitempty"` // The zero-based index ordering the display of the Parameters in a UI.
}

// Type of data found in a Parameter.
type ParameterDataType = string

// Strongly typed list of Parameter Data Types.
var ParameterDataTypes = struct {
	String  string
	Integer string
	Boolean string
	Enum    string

	SensorID       string
	OrgID          string
	Platform       string
	Architecture   string
	SensorSelector string

	Duration string
	Time     string

	JSON string
	YAML string

	Object string
}{
	String:  "string",
	Integer: "integer",
	Boolean: "bool",
	Enum:    "enum",

	SensorID:       "sid",
	OrgID:          "oid",
	Platform:       "platform",
	Architecture:   "architecture",
	SensorSelector: "sensor_selector",

	Duration: "duration",
	Time:     "time",

	JSON: "json",
	YAML: "yaml",

	Object: "object",
}

// Examples of full schemas for something like a Yara Scanning Extension:
// {
// 	"scan": {
// 		"is_user_facing": true,
// 		"short_description": "scan a sensor",
// 		"long_description": "actively scan a sensor with a specified yara signature",
// 		"parameters": {
// 			"sensor": {
// 				"is_required": false,
// 				"data_type": "sensor_selector",
// 				"default_value": "*",
// 				"display_index": 0,
// 			},
// 			"signature_names": {
// 				"is_required": true,
// 				"is_list": true,
// 				"data_type": "string",
// 				"display_index": 1,
// 			},
// 			"time_to_live": {
// 				"is_required": false,
// 				"data_type": "duration",
// 				"default_value": "60m",
// 				"display_index": 2,
// 			}
// 		},
// 	},
// 	"log_detection": {
// 		"is_user_facing": false,
// 		"short_description": "report a detection from scan",
// 		"long_description": "report all relevant detections found during a previous scan",
// 		"parameters": {
// 			"sensor": {
// 				"is_required": true,
// 				"data_type": "sid",
// 			},
// 			"detection": {
// 				"is_required": true,
// 				"data_type": "json",
// 			},
// 		},
// 	},
// }
