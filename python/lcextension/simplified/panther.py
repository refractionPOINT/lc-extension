import lcextension
import limacharlie
import os

# The users are expected to populate the following
# environment variables.
# RULES_DIR = directory where the rule files are stored.
# EXTENSION_NAME = name of the extension.
# EVENT_TYPES = list of event types to subscribe to, comma separated.

class PantherExtension(lcextension.Extension):
    def init(self):

        self.extension_name = os.getenv('EXTENSION_NAME', 'panther')
        self.event_types = [e.strip() for e in os.getenv('EVENT_TYPES', '').split(',')]
        self.configSchema = lcextension.SchemaObject()
        self.requestSchema = lcextension.RequestSchemas()
        self.requestSchema.Actions = {
            'run': lcextension.RequestSchema(
                Label = 'Run Rules',
                IsUserFacing = True,
                ShortDescription = "Run rules",
                LongDescription = "Run a specific playbook.",
                IsImpersonated = False,
                ParameterDefinitions = lcextension.SchemaObject(
                    Fields = {
                        "event": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.Object,
                            DisplayIndex = 1,
                            Description = "the event from limacharlie",
                        ),
                        "routing": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.Object,
                            DisplayIndex = 1,
                            Description = "the routing from limacharlie",
                        ),
                    },
                    Requirements = [["event"]],
                ),
            ),
        }

        # Request handlers receive:
        # - SDK: limacharlie.Manager()
        # - Request Data: {}
        # - Extension Configuration: {}
        # - Resource State {}
        self.requestHandlers = {
            'run': self.handleRun,
        }
        # Event handlers receive:
        # - SDK: limacharlie.Manager()
        # - Event Data: {}
        # - Extension Configuration: {}
        self.eventHandlers = {
            'subscribe': self.handleSubscribe,
            'unsubscribe': self.handleUnsubscribe,
        }

        # Load rules from the RULES_DIR directory.
        self.panther_rules = {}
        rules_dir = os.getenv('RULES_DIR', 'rules')
        for file_name in os.listdir(rules_dir):
            if file_name.endswith('.py'):
                with open(os.path.join(rules_dir, file_name), 'r') as f:
                    rule_name = file_name[:-3]
                    self.panther_rules[rule_name] = f.read()
        # For each rule, evaluate the content as python so we
        # can get some of the global elements.
        for rule_name, rule_content in self.panther_rules.items():
            new_namespace = {}
            exec(rule_content, new_namespace, {})
            self.panther_rules[rule_name] = new_namespace
        
        self.log(f"initialized: extension_name={self.extension_name} event_types={self.event_types} and rules={len(self.panther_rules)}")


    def validateConfig(self, sdk, conf):
        # If this function generates an Exception() it will
        # be reported as a failure to validate for LimaCharlie.
        pass

    def handleSubscribe(self, sdk, data, conf):
        try:
            opt_mapping = {"event_type_path": r"panther_detection"}
            self.create_extension_adapter(sdk, opt_mapping)
        except Exception as e:
            return lcextension.Response(error=f'failed extension adapter creation: {e}')
        
        # Create the D&R rule to trigger on all the target event types
        # and send an extension request to run the rules.
        hive = limacharlie.Hive(sdk, 'dr-managed', sdk._oid)

        data = {
            "usr_mtd":{
                "enabled": True,
                "tags": ["lc:system", self.get_extension_private_tag()]
            }, 
            "data":{
                "detect": {
                    "events": self.event_types,
                    "op": "exists",
                    "path": "event",
                }, 
                "respond": [{
                    "action": "extension request",
                    "extension name": self.extension_name,
                    "extension action": "run",
                    "extension request": {
                        "event": "event",
                        "routing": "routing",
                    }
                }]
            }
        }
        try:
            hive_record = limacharlie.HiveRecord(f"panther-{self.extension_name}", data)
            hive.set(hive_record)
        except Exception as e:
            raise limacharlie.LcApiException(f"failed to create detect response for run : {e}")

        self.log(f"subscribed: org={sdk._oid} data={data} conf={conf}")
        return lcextension.Response()

    def handleUnsubscribe(self, sdk, data, conf):
        try:
            self.delete_extension_adapter(sdk)
        except Exception as e:
            self.log(f'failed to delete extension adapter : {e}')
        
        hive = limacharlie.Hive(sdk, 'dr-managed', sdk._oid)
        try:
            hive.delete(f"panther-{self.extension_name}")
        except Exception as e:
            self.log(f"failed to delete detect response for run : {e}")

        self.log(f"unsubscribed: org={sdk._oid} data={data} conf={conf}")
        return lcextension.Response()

    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")

    def handleRun(self, sdk, data, conf, res_state):
        event = data.get('event', None)
        routing = data.get('routing', None)
        evt = {
            "event": event,
            "routing": routing,
        }
        if not event:
            return lcextension.Response(error="missing event")
        # Pass the event to all the rules.
        # The panther rule structure is defined here: https://docs.panther.com/detections/rules/python
        for rule_name, rule_content in self.panther_rules.items():
            is_match = False
            try:
                is_match = rule_content['rule'](evt)
            except Exception as e:
                self.log(f"failed to run rule {rule_name}: {e}")
                continue
            if not is_match:
                continue
            if 'title' in rule_content:
                title = rule_content['title'](evt)
            else:
                title = rule_name
