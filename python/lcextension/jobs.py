import uuid
import time
import yaml
import json
import base64

class Job( object ):
    '''Abstraction for reporting Job updates to LimaCharlie.'''

    def __init__( self, jobId = None ):
        '''Instantiate a new job or open an existing job for update.
        :param jobId: optional JobID to open for update.
        '''
        self._isNew = False
        self._json = {}
        if jobId is None:
            self._isNew = True
            jobId = str( uuid.uuid4() )
            self._json[ 'start' ] = int( time.time() * 1000 )
        self._json[ 'id' ] = jobId

    def getId( self ):
        '''Get the job's ID.
        '''
        return self._json[ 'id' ]

    def addSensor( self, sid ):
        '''Add a sensor ID to this job, indicating it is somehow involved in the job.
        :param sid: sensor ID to add.
        '''
        self._json.setdefault( 'sid', [] ).append( str( sid ) )

    def setCause( self, cause ):
        '''Set the cause for the creation of the job.
        :param cause: the cause string to set.
        '''
        self._json[ 'cause' ] = cause

    def close( self ):
        '''Indicate the job is now finished.
        '''
        self._json[ 'end' ] = int( time.time() * 1000 )

    def toJson( self ):
        if self._isNew and 'cause' not in self._json:
            raise Exception( '"cause" is required for new jobs' )
        return self._json

    def narrate( self, message, attachments = [], isImportant = False ):
        '''Give an update message to the job.
        :param message: simple message describing the update.
        :param attachments: optional list of attachments add along this update.
        :param isImportant: if True, this update will be highlighted in the job log as particularly important.
        '''
        self._json.setdefault( 'hist', [] ).append( {
            'ts' : int( time.time() * 1000 ),
            'msg' : str( message ),
            'attachments' : [ a.toJson() for a in attachments ],
            'is_important' : bool( isImportant ),
        } )

    def __str__( self ):
        return json.dumps( self.toJson(), indent = 2 )

    def __repr__( self ):
        return json.dumps( self.toJson(), indent = 2 )

class HexDump( object ):
    '''Abstraction for adding an attachment to a Job update to be displayed as a hex dump.'''

    def __init__( self, caption, data ):
        '''Create a new hex dump attachment object.
        :param caption: small blurb describing the dump content.
        :param data: binary data to display in the dump.
        '''
        self._data = {
            'att_type' : 'hex_dump',
            'caption' : str( caption ),
            'data' : base64.b64encode( data ),
        }

    def toJson( self ):
        return self._data

class Table( object ):
    '''Abstraction for adding an attachment to a Job update to be displayed as a table.'''

    def __init__( self, caption, headers, rows = [] ):
        '''Create a new table attachment object.
        :param caption: small blurb describing the table content.
        :param headers: list of of the table's column titles.
        :param rows: list of list of items representing all the rows of the table.
        '''
        if not isinstance( headers, ( list, tuple ) ):
            raise Exception( "Table headers must be a list or tuple, not %s" % ( type( headers ), ) )
        self._data = {
            'att_type' : 'table',
            'caption' : str( caption ),
            'headers' : headers,
            'rows' : [],
        }
        for r in rows:
            self.addRow( r )

    def addRow( self, fields ):
        '''Add a row to the table.
        :param fields: list of fields representing a single table row.
        '''
        if not isinstance( fields, ( list, tuple ) ):
            raise Exception( "Table row must be list or tuple, not %s" % ( type( fields ), ) )
        self._data[ 'rows' ].append( fields )

    def length( self ):
        '''Get the number of rows in the table.
        '''
        return len( self._data[ 'rows' ] )

    def toJson( self ):
        return self._data

class YamlData( object ):
    '''Abstraction for adding an attachment to a Job update to be displayed as YAML data.'''

    def __init__( self, caption, data ):
        '''Create a new YAML attachment object.
        :param caption: small blurb describing the yaml content.
        :param data: data to display in the attachment.
        '''
        if not isinstance( data, ( list, tuple, dict ) ):
            raise Exception( "YAML data must be a dict/list/tuple." )

        self._data = {
            'att_type' : 'yaml',
            'caption' : caption,
            'data' : yaml.safe_dump( data, default_flow_style = False ),
        }

    def toJson( self ):
        return self._data

class JsonData( object ):
    '''Abstraction for adding an attachment to a Job update to be displayed as a JSON data.'''

    def __init__( self, caption, data ):
        '''Create a new JSON attachment object.
        :param caption: small blurb describing the JSON content.
        :param data: data to display in the attachment.
        '''
        if not isinstance( data, ( list, tuple, dict ) ):
            raise Exception( "JSON data must be a dict/list/tuple." )

        self._data = {
            'att_type' : 'json',
            'caption' : caption,
            'data' : json.dumps( data, indent = 2 ),
        }

    def toJson( self ):
        return self._data