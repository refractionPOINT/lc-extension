import os

import sys
global_helpers = os.path.join(os.path.dirname(__file__), "global_helpers")
if global_helpers not in sys.path:
    sys.path.insert(0, global_helpers)

from .panther import PantherExtension
app = PantherExtension(f"ext-panther-{os.getenv('EXTENSION_NAME', None)}", os.environ["LC_SHARED_SECRET"]).getApp()
