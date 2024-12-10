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

        self.extension_name = os.getenv('EXTENSION_NAME', None)
        if not self.extension_name:
            raise Exception("missing EXTENSION_NAME environment variable")
        self.event_types = [e.strip() for e in os.getenv('EVENT_TYPES', '').split(',')]
        if len(self.event_types) == 0:
            raise Exception("missing EVENT_TYPES environment variable")
        self.configSchema = lcextension.SchemaObject()
        self.requestSchema = lcextension.RequestSchemas()
        self.requestSchema.Actions = {
            'run': lcextension.RequestSchema(
                Label = 'Run Rules',
                IsUserFacing = False,
                ShortDescription = "Run rules",
                LongDescription = "Run a set of rules.",
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
                            DisplayIndex = 2,
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
        if not event:
            return lcextension.Response(error="missing event")
        
        # Wrap the event in a pantherEvent object that rules can use.
        event = pantherEvent(event)
        
        # Pass the event to all the rules.
        # The panther rule structure is defined here: https://docs.panther.com/detections/rules/python
        for rule_name, rule_content in self.panther_rules.items():
            is_match = False
            try:
                is_match = rule_content['rule'](event)
            except Exception as e:
                self.log(f"failed to run rule {rule_name}: {e}")
                continue
            if not is_match:
                continue
            if 'title' in rule_content:
                title = rule_content['title'](event)
            else:
                title = rule_name
            if 'alert_context' in rule_content:
                alert_context = rule_content['alert_context'](event)
            else:
                alert_context = {}
            if 'severity' in rule_content:
                severity = rule_content['severity'](event)
            else:
                severity = "DEFAULT"

            # Report the detection via the extension adapter.
            self.send_to_webhook_adapter(sdk, {
                "event": event,
                "routing": routing,
                "title": title,
                "severity": severity,
                "alert_context": alert_context,
            })

# The exact event implementation doesn't seem provided in the
# open source repo, so we will build one inspired by parts of
# the repo: https://github.com/panther-labs/panther-analysis/blob/8e38626795a1c8ade822fd00e336e9998d4977dc/global_helpers/panther_base_helpers.py#L30
from collections.abc import Mapping
from functools import reduce
class pantherEvent(dict):
    def deep_get(self, *keys, default=None):
        """Safely return the value of an arbitrarily nested map

        Inspired by https://bit.ly/3a0hq9E
        """
        out = reduce(
            lambda d, key: d.get(key, default) if isinstance(d, Mapping) else default, keys, self
        )
        if out is None:
            return default
        return out