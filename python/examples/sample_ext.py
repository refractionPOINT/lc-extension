import lcextension

class SampleExtension(lcextension.Extension):
    def init(self):
        self.schema = lcextension.SchemaDefinition()

    def validateConfig(self, sdk, conf):
        # If this function generates an Exception() it will
        # be reported as a failure to validate for LimaCharlie.
        pass

    def handleRequest(self, sdk, action, data, conf):
        return lcextension.Response()

    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")
