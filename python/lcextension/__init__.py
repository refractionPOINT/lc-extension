"""Reference implementation for LimaCharlie.io extensions."""

__version__ = "1.1.10"
__author__ = "Maxime Lamothe-Brassard ( Refraction Point, Inc )"
__author_email__ = "maxime@refractionpoint.com"
__license__ = "Apache v2"
__copyright__ = "Copyright (c) 2023 Refraction Point, Inc"

_PROTOCOL_VERSION = 20221218

from .ext import Extension
from .messages import *
from .schema import *
