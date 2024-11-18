import lcextension
import limacharlie
from typing import Callable

# Users are expected to subclass PlaybookExtension and implement the
# following methods:
# - getPlaybooks: returns a list of Playbook objects.

class PlaybookExtension(lcextension.Extension):
    def init(self):

        self.configSchema = lcextension.SchemaObject()
        self.requestSchema = lcextension.RequestSchemas()
        self.requestSchema.Actions = {
            'run': lcextension.RequestSchema(
                Label = 'Run playbook',
                IsUserFacing = True,
                ShortDescription = "Run Playbook",
                LongDescription = "Run a specific playbook.",
                IsImpersonated = False,
                ParameterDefinitions = lcextension.SchemaObject(
                    Fields = {
                        "playbook_name": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.String,
                            DisplayIndex = 1,
                            Description = "the playbook to run",
                        ),
                        "step": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.String,
                            DisplayIndex = 2,
                            Description = "the step to run",
                        ),
                        "data": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.Object,
                            DisplayIndex = 3,
                            Description = "the data to pass to the step",
                        ),
                    },
                    Requirements = [["playbook_name"], ["step"]],
                ),
            ),
        }

        # Request handlers receive:
        # - SDK: limacharlie.Manager()
        # - Request Data: {}
        # - Extension Configuration: {}
        # - Resource State {}
        self.requestHandlers = {
            'run': self.handlePlaybook,
        }
        # Event handlers receive:
        # - SDK: limacharlie.Manager()
        # - Event Data: {}
        # - Extension Configuration: {}
        self.eventHandlers = {
            'subscribe': self.handleSubscribe,
            'unsubscribe': self.handleUnsubscribe,
        }

        # Resolve the playbook names to a dictionary for easy lookup.
        self.playbooks = {}
        for playbook in self.getPlaybooks():
            # If there is a duplicate playbook name, warn the user.
            if playbook.name in self.playbooks:
                self.logCritical(f"Duplicate playbook name: {playbook.name}")
            self.playbooks[playbook.name] = playbook

    def validateConfig(self, sdk, conf):
        # If this function generates an Exception() it will
        # be reported as a failure to validate for LimaCharlie.
        pass

    def handleSubscribe(self, sdk, data, conf):
        try:
            opt_mapping = {"event_type_path": r"{{.playbook_name}}-{{.step}}"}
            self.create_extension_adapter(sdk, opt_mapping)
        except Exception as e:
            return lcextension.Response(error=f'failed extension adapter creation: {e}')

        self.log(f"subscribed: org={sdk._oid} data={data} conf={conf}")
        return lcextension.Response()
    
    def handleUnsubscribe(self, sdk, data, conf):
        try:
            self.delete_extension_adapter(sdk)
        except Exception as e:
            self.log(f'failed to delete extension adapter : {e}')

        self.log(f"unsubscribed: org={sdk._oid} data={data} conf={conf}")
        return lcextension.Response()
    
    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")
    
    def handlePlaybook(self, sdk, data, conf, res_state):
        self.log(f"running playbook: org={sdk._oid} data={data} conf={conf}")
        playbook_name = data.get('playbook_name')
        step = data.get('step')
        if not playbook_name:
            return lcextension.Response(error = "missing playbook_name")
        if not step:
            return lcextension.Response(error = "missing step")
        playbook = self.playbooks.get(playbook_name)
        if not playbook:
            return lcextension.Response(error = f"playbook not found: {playbook_name}")
        playbook_step = playbook.steps.get(step)
        if not playbook_step:
            return lcextension.Response(error = f"step not found: {step}")
        return playbook_step(sdk, data.get('data', {}), lambda log_data: self.logPlaybook(log_data))
    
    def logPlaybook(self, log_data):
        self.log(log_data)

# The bit users care about:
# =============================================================================
PlaybookLogger = Callable[[dict], None]
PlaybookStep = Callable[[limacharlie.Manager, dict, PlaybookLogger], lcextension.Response]

class Playbook(object):
    def __init__(self, name: str, description: str, steps: dict[str, PlaybookStep]):
        self.name = name
        self.description = description
        self.steps = steps
