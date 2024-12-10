# Panther Compatibility
This module implements a compatibility mode to run Panther-style rules as-is
within a LC Extension.

Example one-line to just run it locally:
```
LC_SHARED_SECRET=aaabbb EVENT_TYPES=app_scopes_expanded,app_resources_added EXTENSION_NAME=panther-test python3 -m gunicorn -w 1 -b 127.0.0.1:8484 lcextension.simplified.panther:app
```