# Cloud Run Multiplexer

This multiplexer receives webhooks from LimaCharlie and distributes them to the appropriate Extension.
It does this at the OID level, meaning that if you have multiple OIDs subscribed to the same Extension, they will all receive the same Extension.
The multiplexer also proxies the webhook to the Extension, meaning that the Extension will receive the webhook as if it came from LimaCharlie directly.

The multiplexer creates a new Cloud Run service for each new Organization that subscribes to an Extension, and deletes the service when the Organization unsubscribes.

The multiplexer is designed as an Extension itself and is designed to be deployed as-is without subclassing through environment variables only.