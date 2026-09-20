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
- ACL: for Requests, a platform-set view of what the initiator may access with respect to LimaCharlie resource ACLs (see below).

### Resource ACLs on Requests
LimaCharlie resource ACLs restrict the *content* of a resource (a sensor, a hive record...) by tagging it `acl:<scope>`: only principals holding the scope `<scope>` may read it. Multiple `acl:` tags on one resource are ANDed.

To let an Extension honor those ACLs, LimaCharlie adds a signed, top-level `acl` block to every `request` envelope it sends (it is never inside `data`, which is user-controlled):

```json
"request": {
  "org": {...}, "action": "...", "data": {...}, "config": {...},
  "acl": {
    "scopes":  ["hr", "finance"],
    "global":  false,
    "enforce": true,
    "source":  "user"
  }
}
```
- `scopes`: scope names the initiator holds (normalized, no `acl:` prefix).
- `global`: the initiator holds `access.global` and is allowed everywhere.
- `enforce`: the org has at least one enabled scope. `false` means nothing is restricted and the Extension may skip every ACL check for this request.
- `source`: one of `user`, `impersonated`, `dr`, `continuation`, `internal`. Informational only.

In Go the block is exposed as `params.ACL` (a `common.ACLView`) on `RequestCallbackParams`:
- `params.ACL.Allows(tags []string) bool` applies the platform rule: true when `Global`, true when `!Enforce`, otherwise every `acl:` tag in `tags` must name a held scope. A bare `acl:` or an unknown scope locks the resource; tags without the `acl:` prefix are ignored; comparison is case-insensitive with whitespace trimmed and comma-joined entries are split.
- `params.ACL.Present() bool` reports whether the envelope carried the block.

**Fail-closed default:** when the block is absent (for instance an older LimaCharlie extension manager), the view is `Scopes: nil, Global: false, Enforce: true, Present: false`. `Allows` then returns `false` for any resource carrying an `acl:` tag and `true` for untagged ones, so an Extension that enforces ACLs can never leak a restricted resource because the platform did not say what the initiator holds.

`Allows` only ever restricts: use it in addition to your normal permission checks, never to grant access the org permissions would not already allow. The Python SDK exposes the same thing as `msg_request.acl` (an `lcextension.ACL` with `allows(tags)` and `present`), passed as an optional fifth argument to request handlers that declare one.

### Resource ACLs and the Rules your Extension Installs
If your Extension writes D&R rules into the organization, for example a rule that sends a sensor's reply back to one of your Actions or one that runs on a schedule, give those rules an `acl_scopes` list containing `*`, next to `detect` and `respond`:

```yaml
detect:
  ...
respond:
  - action: extension request
    extension name: my-extension
    extension action: process
    extension request: {}
acl_scopes:
  - '*'
```

On a sensor restricted by a resource ACL, the platform refuses `extension request`, `service request` and `start ai agent` unless the rule's `acl_scopes` covers the sensor's scopes. Your Extension cannot know an organization's scope names, so it lists `*`, which stands for the scopes your Extension's API key is a member of at the moment the rule fires. The organization's administrator decides what your Extension can reach by adding its key to a scope, or removing it.

- Write these rules with the organization client your callbacks receive, so the rule's author is your Extension's key. `*` is only accepted from an org API key.
- The platform adds an `acl_scopes_author` field to the stored rule. If you read your rules back and compare them with what you wrote, ignore that field.
- Rules that only `report`, `task`, tag, etc. need no `acl_scopes`.

In the Go `simplified` package:
- The recurring update rule that `RuleExtension` and `LookupExtension` install lists `*`. One installed before this existed gets it on the next update.
- The rules your `GetRules` callback returns are written as you supply them: add `acl_scopes` to the ones that use one of the three actions above. `acl_scopes_author` is ignored when a stored rule is compared with yours, so such a rule is not rewritten on every update.
- A platform that does not know `*` yet refuses the rule with an error naming `acl_scopes`. The rule is then written without `acl_scopes`, with a warning, so your Extension keeps working as it did before (without reaching restricted sensors). No other failure is handled this way, in particular not `ACL_SCOPES_UNAVAILABLE`, which is temporary.

The Python SDK has no helper that installs rules: a Python Extension that writes its own rules sets `acl_scopes` on them the same way.

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