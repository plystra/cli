# Account Profile plugin

Plugin ID: `acme.my-app.account-profile`

This is a root-level plugin in the `example.com/acme/my-app/v2` Go Module. Its declarative source is `plugin.yaml`, and generated configuration and assembly bindings are committed under the module's `generated/` directory.

## Capabilities

This plugin starts without capabilities. Add a custom capability with:

```text
plystra capability create <namespace.operation>
```

Implement an existing canonical capability with:

```text
plystra capability implement <namespace.operation/vN>
```
