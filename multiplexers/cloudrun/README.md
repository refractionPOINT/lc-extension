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
  "ping": {
    "is_user_facing": true,
    "short_description": "simple ping request",
    "long_description": "will echo back some value",
    "is_impersonated": false,
    "parameter_definitions": {
      "fields": {
        "some_value": {
          "is_list": false,
          "data_type": "string",
          "display_index": 1
        }
      },
      "requirements": [
        ["some_value"]
      ]
    },
    "response_definition": {
      "fields": {
        "some_value": {
          "description": "same value as received",
          "data_type": "string"
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
[]
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
