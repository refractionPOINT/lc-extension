import lcextension

class SampleExtension(lcextension.Extension):
    def init(self):
        self.schema = lcextension.SchemaDefinition()

    def validateConfig(self, sdk, conf):
        pass

    def handleRequest(self, sdk, action, data, conf):
        return lcextension.Response()

    def handleError(self, oid, error):
        self.logCritical(f"received error from limacharlie for {oid}: {error}")
