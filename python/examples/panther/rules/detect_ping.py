
def rule(event):
    return event.get("FILE_PATH").lower().endswith("\\ping.exe")


def title(event):
    return (
        f"Found a ping execution in the file path: {event.get('FILE_PATH')}"
    )


def alert_context(event):
    context = {
        "user": event.deep_get("USER_NAME", default="unknown-user"),
        "cmdline": event.deep_get("CMD_LINE", default="unknown-cmdline"),
    }

    return context


def severity(event):
    return "Medium"
