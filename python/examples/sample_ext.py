import lcextension

class SampleExtension(lcextension.Extension):
    def init(self):

        self.configSchema = lcextension.SchemaObject()
        self.requestSchema = lcextension.RequestSchemas()
        self.requestSchema.Actions = {
            'ping': lcextension.RequestSchema(
                IsUserFacing = True,
                ShortDescription = "simple ping request",
                LongDescription = "will echo back some value",
                IsImpersonated = False,
                ParameterDefinitions = lcextension.SchemaObject(
                    Fields = {
                        "some_value": lcextension.SchemaElement(
                            IsList = False,
                            DataType = lcextension.SchemaDataTypes.STRING,
                            DisplayIndex = 1,
                        ),
                    },
                    Requirements = [["some_value"]],
                ),
                ResponseDefinition = lcextension.SchemaObject(
                    Fields = {
                        "some_value": lcextension.SchemaElement(
                            Description = "same value as received",
                            DataType = lcextension.SchemaDataTypes.STRING,
                        ),
                    }
                ),
            ),
        }
        self.requestHandlers = {
            'ping': self.handlePing,
        }
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
        return lcextension.Response()
    
    def handleUnsubscribe(self, sdk, data, conf):
        return lcextension.Response()

    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")

def __main__():
    SampleExtension("basic-extension", "1234").run()