import lcextension

class SampleExtension(lcextension.Extension):
    def init(self):

        self.configSchema = lcextension.SchemaObject()    # The shape of the configuration objects.
        self.requestSchema = lcextension.RequestSchemas() # The shape of requests that are accepted.
        self.requestSchema.Actions = { # Action Name => Definition
            'ping': lcextension.RequestSchema(
                IsUserFacing = True, # Should this Action be displayed in the webapp?
                ShortDescription = "simple ping request",
                LongDescription = "will echo back some value",
                IsImpersonated = False, # If False, SDK received is for the Extension, else for the caller?
                ParameterDefinitions = lcextension.SchemaObject(
                    Fields = {
                        "some_value": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.String,
                            DisplayIndex = 1,
                        ),
                    },
                    Requirements = [["some_value"]],
                ),
                ResponseDefinition = lcextension.SchemaObject(
                    Fields = {
                        "some_value": lcextension.SchemaElement(
                            Description = "same value as received",
                            DataType = lcextension.SchemaDataTypes.String,
                        ),
                    }
                ),
            ),
        }
        # Request handlers receive:
        # - SDK: limacharlie.Manager()
        # - Request Data: {}
        # - Extension Configuration: {}
        self.requestHandlers = {
            'ping': self.handlePing,
        }
        # Event handlers receive:
        # - SDK: limacharlie.Manager()
        # - Event Data: {}
        # - Extension Configuration: {}
        self.eventHandlers = {
            'subscribe': self.handleSubscribe,
            'unsubscribe': self.handleUnsubscribe,
        }

    def validateConfig(self, sdk, conf):
        # If this function generates an Exception() it will
        # be reported as a failure to validate for LimaCharlie.
        pass

    def handlePing(self, sdk, data, conf):
        return lcextension.Response(data = data)
    
    def handleSubscribe(self, sdk, data, conf):
        self.log(f"new organization has subscribed: data={data} conf={conf}")
        return lcextension.Response()
    
    def handleUnsubscribe(self, sdk, data, conf):
        self.log(f"new organization has unsubscribed: data={data} conf={conf}")
        return lcextension.Response()

    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")

def __main__():
    SampleExtension("basic-extension", "1234").run()