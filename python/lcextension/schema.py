class SchemaObject(object):
    def __init__(self, **kwargs):
        self.Fields = {} # {} of Field name to SchemaElement
        self.Key = None
        self.ListElementName = None
        self.ElementDescription = None
        self.Requirements = [] # [] of [] of Field names

        # legacy
        self.RenderType = None
        self.KeyDataType = None # SchemaDataTypes
        self.KeyLabel = None # SchemaDataTypes
        self.KeyDisplayIndex = None # SchemaDataTypes
        self.KeyName = None
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self):
        return {
            'fields' : {n: f.serialize() for n, f in self.Fields.items()},
            'list_element_name': self.ListElementName,
            'element_desc': self.ElementDescription,
            'key': self.Key,
            'requirements' : self.Requirements,

            # legacy
            'render_type' : self.RenderType,
            'key_data_type' : self.KeyDataType,
            'key_name' : self.KeyName,
            'key_label' : self.KeyLabel,
            'key_display_index' : self.KeyDisplayIndex,
        }

class SchemaElement(object):
    def __init__(self, **kwargs):
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
        self.Filter = {}
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self):
        return {
            'label' : self.Label,
            'description' : self.Description,
            'data_type' : self.DataType,
            'is_list' : self.IsList,
            'display_index' : self.DisplayIndex,
            'default_value' : self.DefaultValue,
            'object' : None if self.ObjectSchema is None else self.ObjectSchema.serialize(),
            'enum_values' : self.EnumValues,
            'placeholder' : self.PlaceHolder,
            'supported_actions' : self.SupportedActions,
            'filter': self.Filter,
        }

class RequestSchemas(object):
    def __init__(self):
        self.Actions = {} # {} of Action name to RequestSchema

    def serialize(self):
        return {n : a.serialize() for n, a in self.Actions.items()}

class RequestSchema(object):
    def __init__(self, **kwargs):
        self.IsDefaultRequest = False
        self.IsUserFacing = True
        self.ShortDescription = ""
        self.LongDescription = ""
        self.IsImpersonated = False
        self.ParameterDefinitions = SchemaObject()
        self.ResponseDefinition = None # SchemaObject
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self):
        return {
            'is_default' : self.IsDefaultRequest,
            'is_user_facing' : self.IsUserFacing,
            'short_description' : self.ShortDescription,
            'long_description' : self.LongDescription,
            'is_impersonated' : self.IsImpersonated,
            'parameters' : self.ParameterDefinitions.serialize(),
            'response' : None if self.ResponseDefinition is None else self.ResponseDefinition.serialize(),
        }

class SchemaView(object):
    def __init__(self, **kwargs):
        self.Name = ""
        self.LayoutType = None
        self.DefaultRequests = None # [] of Request names

    def serialize(self):
        return {
            'name' :self.Name,
            'layout_type' : self.LayoutType,
            'default_requests' : self.DefaultRequests,
        }

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
    EventName = 'event_name'
    Tag = "tag"
    Duration = "duration" # milliseconds
    Time = "time" # milliseconds since epoch
    URL = "url"
    Domain = "domain"
    JSON = "json"
    YAML = "yaml"
    YaraRule = "yara_rule"
    YaraRuleName = "yara_rule_name"
    Object = "object"
    Record = "record"