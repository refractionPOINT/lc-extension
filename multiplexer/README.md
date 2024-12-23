# Cloud Run Multiplexer

This multiplexer receives webhooks from LimaCharlie and distributes them to the appropriate Extension.
It does this at the OID level, meaning that if you have multiple OIDs subscribed to the same Extension, they will all receive the same Extension.
The multiplexer also proxies the webhook to the Extension, meaning that the Extension will receive the webhook as if it came from LimaCharlie directly.

The multiplexer creates a new Cloud Run service for each new Organization that subscribes to an Extension, and deletes the service when the Organization unsubscribes.

The multiplexer is designed as an Extension itself and is designed to be deployed as-is without subclassing through environment variables only.

You can use the `multiplexer.Extension.HookSendMessage()` and `multiplexer.Extension.HookResponse()` hooks to add custom processing to the multiplexer.

## Example configs

The definition of the service to create in Cloud Run. The shared secret for those services is dynamically
generated and set as `LC_SHARED_SECRET` in the new service.
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
    "SOME_ENV_VAR=somevalue"
  ],
  "service_account": "my-service-account@myproject.iam.gserviceaccount.com"
}
```

`SHARED_SECRET`: `1234`

`PROVISION_PROJECT_ID`: `my-project`

`PROVISION_REGION`: `us-central1`

`LC_REFERENCE_SHARED_SECRET`: `1234`

`LC_REFERENCE_SERVICE_URL`: `https://my-reference-service.com`