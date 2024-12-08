import os
import lcextension.simplified

class SimplePlaybook(lcextension.simplified.PlaybookExtension):
    
    def getPlaybooks(self):
        return [
            lcextension.simplified.Playbook(
                "simple-playbook-1",
                "this simple playbook will check if sysmon is intalled and will install it if not",
                {
                    "check-sysmon": self.checkSysmon,
                    "install-sysmon": self.installSysmon,
                },
            ),
        ]
    
    def checkSysmon(self, sdk, data, log):
        log({"message": f"checking if sysmon is installed on {data['sid']}"})
        return lcextension.simplified.Response(data = data)
    
    def installSysmon(self, sdk, data, log):
        log({"message": f"installing sysmon on {data['sid']}"})
        return lcextension.simplified.Response(data = data)

app = SimplePlaybook("simple-playbook", os.environ["LC_SHARED_SECRET"]).getApp()
