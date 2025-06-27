from setuptools import setup

__version__ = "1.1.10"
__author__ = "Maxime Lamothe-Brassard ( Refraction Point, Inc )"
__author_email__ = "maxime@refractionpoint.com"
__license__ = "Apache v2"
__copyright__ = "Copyright (c) 2023 Refraction Point, Inc"

setup( name = 'lcextension',
       version = __version__,
       description = 'Reference implementation for LimaCharlie.io extensions.',
       url = 'https://limacharlie.io',
       author = __author__,
       author_email = __author_email__,
       license = __license__,
       packages = [ 'lcextension' ],
       zip_safe = True,
       install_requires = [ 'limacharlie', 'flask' ],
       long_description = 'Reference implementation for LimaCharlie.io extensions, allowing anyone to extend and automate extensions around LimaCharlie.'
)
