# LimaCharlie Python Extension

## Request handlers and resource ACLs
Request handlers are registered in `self.requestHandlers` and are called as `handler(sdk, data, conf, resState)`.

LimaCharlie attaches a signed, top-level `acl` block to every request envelope describing what the initiator may access with respect to resource ACLs (resources tagged `acl:<scope>`). It is exposed as `msg_request.acl`, an `lcextension.ACL`, and is passed as an optional fifth positional argument to handlers that declare one (existing four-argument handlers keep working):

```python
def handlePing(self, sdk, data, conf, resState, acl=None):
    if acl is not None and not acl.allows(resource['tags']):
        return lcextension.Response(error='not allowed')
    ...
```

- `acl.allows(tags)` is true when `acl.is_global`, true when `not acl.enforce`, otherwise every `acl:` tag in `tags` must name a scope in `acl.scopes` (AND). A bare `acl:` or an unknown scope locks the resource; tags without the `acl:` prefix are ignored; comparison is case-insensitive with whitespace trimmed and comma-joined entries are split.
- `acl.present` reports whether the envelope carried the block.
- `acl.source` is informational (`user`, `impersonated`, `dr`, `continuation`, `internal`).

**Fail-closed default:** when the block is absent (older LimaCharlie extension manager), `acl.present` is `False`, `acl.scopes` is empty, `acl.is_global` is `False` and `acl.enforce` is `True`, so `allows()` returns `False` for any `acl:`-tagged resource and `True` for untagged ones.

`allows()` only ever restricts: use it in addition to your normal permission checks, never to grant access the org permissions would not already allow.

## Rules your extension installs, and resource ACLs

If your extension writes D&R rules into the organization (with the `sdk` your handlers receive), and a rule uses the `extension request`, `service request` or `start ai agent` action, give it an `acl_scopes` list containing `*`, next to `detect` and `respond`:

```python
rule = {
    'detect': {...},
    'respond': [{
        'action': 'extension request',
        'extension name': 'my-extension',
        'extension action': 'process',
        'extension request': {},
    }],
    'acl_scopes': ['*'],
}
```

On a sensor restricted by a resource ACL the platform refuses those three actions unless the rule's `acl_scopes` covers the sensor's scopes. `*` stands for the scopes your extension's API key is a member of at the moment the rule fires, so the organization's administrator decides what your extension can reach by adding its key to a scope, or removing it. `*` is only accepted from an org API key, which is what the `sdk` handed to your handlers uses.

The platform adds an `acl_scopes_author` field to the stored rule: ignore it if you read your rules back and compare them with what you wrote.
