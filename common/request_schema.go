package common

// An Action name to ask the Extension to perform.
type RequestAction = string

// List of Parameters expected per Action.
type RequestSchemas = map[RequestAction]RequestSchema

// A human friendly label for something.
type Label = string

// messages for response status copy
type StatusMessages struct {
	InProgressMessage string `json:"in_progress,omitempty" msgpack:"in_progress,omitempty"`
	SuccessMessage    string `json:"success,omitempty" msgpack:"success,omitempty"`
	ErrorMessage      string `json:"error,omitempty" msgpack:"error,omitempty"`
}

// Shema of expected Parameters for a specific request Action.
type RequestSchema struct {
	Label                Label          `json:"label,omitempty" msgpack:"label,omitempty"`       // (optional) Human friendly name for the request
	IsUserFacing         bool           `json:"is_user_facing" msgpack:"is_user_facing"`         // Is this Action expected to be performed by a human, or is it for automation.
	ShortDescription     string         `json:"short_description" msgpack:"short_description"`   // Short description of what this Action does.
	LongDescription      string         `json:"long_description" msgpack:"long_description"`     // Longer version of the Short Description.
	Messages             StatusMessages `json:"messages,omitempty" msgpack:"messages,omitempty"` // (optional) Customizable text to inform the user
	IsImpersonated       bool           `json:"is_impersonated" msgpack:"is_impersonated"`       // If true, this action requires a JWT token from a user that it will use to impersonate.
	ParameterDefinitions SchemaObject   `json:"parameters" msgpack:"parameters"`                 // List of Parameter Names and their definition.
	ResponseDefinition   *SchemaObject  `json:"response" msgpack:"response"`                     // Schema of the expected Response.
}

// Strongly typed list of Parameter Data Types.
type SchemaDataType = string

var SchemaDataTypes = struct {
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

	YaraRule     string
	YaraRuleName string
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

	YaraRule:     "yara_rule",
	YaraRuleName: "yara_rule_name",
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
