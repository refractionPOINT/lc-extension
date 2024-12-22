# Cloud Run Multiplexer

This multiplexer receives webhooks from LimaCharlie and distributes them to the appropriate Extension.
It does this at the OID level, meaning that if you have multiple OIDs subscribed to the same Extension, they will all receive the same Extension.
The multiplexer also proxies the webhook to the Extension, meaning that the Extension will receive the webhook as if it came from LimaCharlie directly.

The multiplexer creates a new Cloud Run service for each new Organization that subscribes to an Extension, and deletes the service when the Organization unsubscribes.

The multiplexer is designed as an Extension itself and is designed to be deployed as-is without subclassing through environment variables only.

## Example configs

`LC_REQUEST_SCHEMA`:

```json
{
  "run": {
    "is_user_facing": true,
    "label": "Run a CLI command",
    "short_description": "Run a CLI command for a supported tool.",
    "long_description": "Run a CLI command using the limacharlie tool by providing a list of command line parameters to provide to it.",
    "is_impersonated": false,
    "parameters": {
      "fields": {
        "command_line": {
          "is_list": false,
          "label": "Command Line",
          "description": "The command to run.",
          "data_type": "string",
          "display_index": 3
        },
        "command_tokens": {
          "is_list": true,
          "label": "Command Parameters",
          "description": "The command parameters to run as a tokenized list.",
          "data_type": "string",
          "display_index": 4
        },
        "credentials": {
          "is_list": false,
          "label": "Credentials",
          "description": "The credentials to use for the command. A GCP JSON key, a DigitalOcean Access Token or an AWS 'accessKeyID/secretAccessKey' pair.",
          "data_type": "secret",
          "display_index": 1
        },
        "tool": {
          "is_list": false,
          "label": "Tool",
          "description": "The tool provider to use.",
          "data_type": "enum",
          "display_index": 2,
          "enum_values": ["limacharlie"]
        }
      },
      "requirements": [
        ["command_line", "command_tokens"],
        ["credentials"]
      ]
    },
    "response_definition": {
      "fields": {
        "output_list": {
          "description": "The output of the command.",
          "data_type": "object",
          "is_list": true
        },
        "output_dict": {
          "description": "The output of the command.",
          "data_type": "object",
          "is_list": false
        },
        "output_string": {
          "description": "The output of the command.",
          "data_type": "string"
        },
        "status_code": {
          "description": "The status code of the command.",
          "data_type": "integer"
        }
      }
    }
  }
}
```

`LC_CONFIG_SCHEMA`:

```json
{}
```

`LC_VIEW_SCHEMA`:

```json
[{
    "name": "",
    "layout_type": "action",
    "default_requests": ["run"]
}]
```

`LC_REQUIRED_EVENTS`:

```json
[]
```

`SERVICE_DEFINITION`:

```json
{
  "timeout": 300,
  "min_instances": 0,
  "max_instances": 1,
  "cpu": "1",
  "memory": "512Mi",
  "image": "gcr.io/my-project/mycontainer:latest",
  "env": [
    "LC_SHARED_SECRET=aaaaaaaa-aaaa-45aaaa-aaaa-62938a5277a8"
  ],
  "service_account": "my-service-account@myproject.iam.gserviceaccount.com"
}
```

`REDIS_ADDR`: `10.10.10.10:6379`

`SHARED_SECRET`: `1234`

`PROVISION_PROJECT_ID`: `my-project`

`PROVISION_REGION`: `us-central1`

`LC_WORKER_SHARED_SECRET`: `1234`