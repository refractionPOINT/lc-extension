
class SchemaObject(object):
    def __init__(self):
        self.Fields = {} # {} of Field name to SchemaElement
        self.RenderType = None
        self.KeyDataType = None # SchemaDataTypes
        self.KeyName = None
        self.Requirements = [] # [] of [] of Field names

class SchemaElement(object):
    def __init__(self):
        self.Label = ""
        self.Description = ""
        self.DataType = None # SchemaDataTypes
        self.IsList = False
        self.DisplayIndex = None
        self.DefaultValue = None
        self.ObjectSchema = None # SchemaObject
        self.EnumValues = None # [] of string
        self.PlaceHolder = None
        self.SupportedActions = None # [] of Action names

class RequestSchemas(object):
    def __init__(self):
        self.Actions = {} # {} of Action name to RequestSchema

class RequestSchema(object):
    def __init__(self):
        self.IsDefaultRequest = False
        self.IsUserFacing = True
        self.ShortDescription = ""
        self.LongDescription = ""
        self.IsImpersonated = False
        self.ParameterDefinitions = SchemaObject()
        self.ResponseDefinition = None # SchemaObject

class SchemaDataTypes(object):
    String = "string"
    Integer = "integer"
    Boolean = "bool"
    Enum = "enum"
    SensorID = "sid"
    OrgID = "oid"
    Platform = "platform"
    Architecture = "architecture"
    SensorSelector = "sensor_selector"
    Tag = "tag"
    Duration = "duration" # milliseconds
    Time = "time" # milliseconds since epoch
    URL = "url"
    Domain = "domain"
    JSON = "json"
    YAML = "yaml"
    Object = "object"