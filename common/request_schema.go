package common

// An Action name to ask the Extension to perform.
type RequestAction = string

// List of Parameters expected per Action.
type RequestSchemas = map[RequestAction]RequestSchema

// Shema of expected Parameters for a specific request Action.
type RequestSchema struct {
	IsDefaultRequest     bool                        `json:"is_default" msgpack:"is_default"`               // Is the default Request when displaying the state of the Extension.
	IsUserFacing         bool                        `json:"is_user_facing" msgpack:"is_user_facing"`       // Is this Action expected to be performed by a human, or is it for automation.
	ShortDescription     string                      `json:"short_description" msgpack:"short_description"` // Short description of what this Action does.
	LongDescription      string                      `json:"long_description" msgpack:"long_description"`   // Longer version of the Short Description.
	IsImpersonated       bool                        `json:"is_impersonated" msgpack:"is_impersonated"`     // If true, this action requires a JWT token from a user that it will use to impersonate.
	ParameterDefinitions RequestParameterDefinitions `json:"parameters" msgpack:"parameters"`               // List of Parameter Names and their definition.
	ResponseDefinition   *ResponseSchema             `json:"response" msgpack:"response"`                   // Schema of the expected Response.
}

// A Parameter Name.
type RequestParameterName = string

// List of Parameters Definition per Parameter Name.
type RequestParameterDefinitions struct {
	Parameters map[RequestParameterName]RequestParameterDefinition `json:"parameters" msgpack:"parameters"`
	// All field sets must be satisfied.
	// Each field is specifies fields where one and only one must be set.
	Requirements []RequiredFields `json:"requirements" msgpack:"requirements"`
}

// The Definition of a Parameter.
type RequestParameterDefinition struct {
	IsList       bool              `json:"is_list,omitempty" msgpack:"is_list,omitempty"`             // Is this Parameter for a single item, or a list of items?
	DataType     ParameterDataType `json:"data_type" msgpack:"data_type"`                             // The type of values expected.
	DefaultValue interface{}       `json:"default_value,omitempty" msgpack:"default_value,omitempty"` // If a default value should be set for is_required: false Parameters.
	EnumValues   []interface{}     `json:"enum_values,omitempty" msgpack:"enum_values,omitempty"`     // If the type is enum, these are the possible values.
	Description  string            `json:"description" msgpack:"description"`
	PlaceHolder  string            `json:"placeholder" msgpack:"placeholder"`                         // Placeholder to display for this field.
	DisplayIndex int               `json:"display_index,omitempty" msgpack:"display_index,omitempty"` // The zero-based index ordering the display of the Parameters in a UI.
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

	Tag string

	Duration string
	Time     string

	URL    string
	Domain string

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

	Tag: "tag",

	Duration: "duration", // milliseconds
	Time:     "time",     // milliseconds epoch

	URL:    "url",
	Domain: "domain",

	JSON: "json",
	YAML: "yaml",

	Object: "object",
}

// Schema for Responses from Requests
type ResponseSchema struct {
	Fields map[DataKey]DataElement `json:"fields" msgpack:"fields"`
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
// 				"default_value": 3600000,
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
