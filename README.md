# lc-extension
LimaCharlie Extension Framework

An Extension is a standardized way to build functionality on top of LimaCharlie.

Extensions abstract away from the builder the following concerns:
- Keeping of which LC Orgs are subscribed to the Extension.
- Storing credentials for all the LC Orgs subscribed.
- Basic configuration storage.

This way, you can focus on the functionality that sets you apart.

## Basic Structure
Extensions are bits of code you maintain and host, meaning you maintain control of your Intellectual Property.

You can host your Extension anywhere you'd like, the only requirement is for it to:
- Be reachable from the internet over HTTPS
- Have a valid SSL certificate going up to a known Root CA.

We also recommend hosting your Extension somewhere with high availability like:
- Google Cloud Run
- Google Cloud Functions
- AWS Lambda
- AWS Elastic Container Service

Fundamentally, the protocol Extensions use is based over HTTP webhooks, which means they can be implemented as REST applications in a container, or even in serverless environments like Cloud Functions and Lambda.

The main lifecycle of an Extension looks like this:
1. Build the Extension.
1. Register the Extension in LimaCharlie.
  1. You will see heartbeat webhooks start to reach your Extension.
1. Subscribe an Organization (or many) to the Extension.
1. The Extension will receive a webhook indicating the subscription.
1. From this point on, this Org can now interact with the Extension.

## Building
The best way to get started is from the [Basic Example](https://github.com/refractionPOINT/lc-extension/blob/master/examples/basic/main.go).

This example demonstrates the basic schaffolding necessary for the Extension and implements a single action `ping`.

The main mechanism where you'll implement functionality is within callbacks. These callbacks will receive several bits of data allowing you to do your thing:
- Config: this is a dictionary that is stored in LimaCharlie's Hive system. It allows you to support basic configurations for your Extension without having to provision and manage storage.
- Organization: most callbacks will receive and instance of a `limacharlie.Organization` which is the root of the LimaCharlie SDK. This SDK will be pre-authenticated for you for the org who's callback you're receiving. This means you never have to store credentials.
- Idempotent Key: a simple key for each unique callback sent to your Extension, you can use this to deduplicate requests if they are ever retried.
- Request Data: data related to a specific Action you've registered for.

There are two types of opaque bits of data your Extension can leverage:
1. Config
1. Request

Each of those can vary a lot from Extension to Extension, and they also require use interaction. This is where Schemas come in. You should specify the Schema of your Configs and your Requests. The LimaCharlie webapp will use the [Request Schema](https://github.com/refractionPOINT/lc-extension/blob/master/common/request_schema.go) to display an interactive UI for users to make requests. Similarly, the webapp will use the [Config Schema](https://github.com/refractionPOINT/lc-extension/blob/master/common/config_schema.go) to display an interactive UI to configure the Extension.

## Under the Hood
This section is a description of how Extensions work. If you're simply developing a new Extension, you likely don't need to read any of this. If you want to implement your own SDK for a different language on the other hand, this will explain how it works under the hood.

Since Extensions are built around receiving webhooks from LimaCharlie, let's start there.

### Webhooks
Your Extension will receive a webhook from LimaCharlie based on multiple event.

Webhooks are defined starting at the [Message structure](https://github.com/refractionPOINT/lc-extension/blob/master/common/messages.go#L3).

... to be completed...