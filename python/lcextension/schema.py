from typing import Dict, List, Optional, Any, Union

SchemaKey = str
Label = str
SchemaDataType = str

class SchemaObject(object):
    def __init__(self, **kwargs: Any):
        self.Fields: Dict[SchemaKey, 'SchemaElement'] = {} # {} of Field name to SchemaElement
        self.Key: Optional['SchemaRecordKey'] = None
        self.ListElementName: Optional[str] = None
        self.ElementDescription: Optional[str] = None
        self.Requirements: List[List[SchemaKey]] = [] # [] of [] of Field names
        self.SupportedActions: Optional[List[str]] = None # [] of Action names
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self) -> Dict[str, Any]:
        return {
            'fields' : {n: f.serialize() for n, f in self.Fields.items()},
            'list_element_name': self.ListElementName,
            'element_desc': self.ElementDescription,
            'key': None if self.Key is None else self.Key.serialize(),
            'requirements' : self.Requirements,
            'supported_actions': self.SupportedActions,
        }

class SchemaRecordKey(object):
    def __init__(self, **kwargs: Any):
        self.Name: str = ""
        self.Label: str = ""
        self.Description: str = ""
        self.DataType: Optional[SchemaDataType] = None # SchemaDataTypes
        self.DisplayIndex: Optional[int] = None
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self) -> Dict[str, Any]:
        return {
            'name': self.Name,
            'label': self.Label,
            'description': self.Description,
            'data_type': self.DataType,
            'display_index': self.DisplayIndex,
        }

class SchemaElement(object):
    def __init__(self, **kwargs: Any):
        self.Label: str = ""
        self.Description: str = ""
        self.DataType: Optional[SchemaDataType] = None # SchemaDataTypes
        self.IsList: bool = False
        self.DisplayIndex: Optional[int] = None
        self.DefaultValue: Optional[Any] = None
        self.ObjectSchema: Optional[SchemaObject] = None # SchemaObject
        self.EnumValues: Optional[List[Union[str, Dict[str, Any]]]] = None # [] of string
        self.PlaceHolder: Optional[str] = None
        self.Filter: Dict[str, Any] = {}
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self) -> Dict[str, Any]:
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
            'filter': self.Filter,
        }

class RequestSchemas(object):
    def __init__(self):
        self.Actions: Dict[str, 'RequestSchema'] = {} # {} of Action name to RequestSchema

    def serialize(self) -> Dict[str, Any]:
        return {n : a.serialize() for n, a in self.Actions.items()}

class RequestSchema(object):
    def __init__(self, **kwargs: Any):
        self.IsDefaultRequest: bool = False
        self.IsUserFacing: bool = True
        self.ShortDescription: str = ""
        self.LongDescription: str = ""
        self.IsImpersonated: bool = False
        self.ParameterDefinitions: SchemaObject = SchemaObject()
        self.ResponseDefinition: Optional[SchemaObject] = None # SchemaObject
        self.Label: str = ""
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self) -> Dict[str, Any]:
        return {
            'is_default' : self.IsDefaultRequest,
            'is_user_facing' : self.IsUserFacing,
            'short_description' : self.ShortDescription,
            'long_description' : self.LongDescription,
            'is_impersonated' : self.IsImpersonated,
            'parameters' : self.ParameterDefinitions.serialize(),
            'response' : None if self.ResponseDefinition is None else self.ResponseDefinition.serialize(),
            'label': self.Label
        }

class SchemaView(object):
    def __init__(self, **kwargs: Any):
        self.Name: str = ""
        self.LayoutType: Optional[str] = None
        self.DefaultRequests: Optional[List[str]] = None # [] of Request names
        for k, v in kwargs.items():
            if not hasattr(self, k):
                raise Exception(f"unknown attribute {k}")
            setattr(self, k, v)

    def serialize(self) -> Dict[str, Any]:
        return {
            'name' :self.Name,
            'layout_type' : self.LayoutType,
            'default_requests' : self.DefaultRequests,
        }

class SchemaDataTypes(object):
    String: str = "string"
    Integer: str = "integer"
    Boolean: str = "bool"
    Enum: str = "enum"
    ComplexEnum: str = "complex_enum"
    Secret: str = "secret"
    SensorID: str = "sid"
    OrgID: str = "oid"
    Platform: str = "platform"
    Architecture: str = "architecture"
    SensorSelector: str = "sensor_selector"
    EventName: str = 'event_name'
    Tag: str = "tag"
    Duration: str = "duration" # milliseconds
    Time: str = "time" # milliseconds since epoch
    URL: str = "url"
    Domain: str = "domain"
    JSON: str = "json"
    YAML: str = "yaml"
    Code: str = "code"
    YaraRule: str = "yara_rule"
    YaraRuleName: str = "yara_rule_name"
    Object: str = "object"
    Record: str = "record"
