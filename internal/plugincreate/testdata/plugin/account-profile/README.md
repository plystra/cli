# Account Profile plugin

Plugin ID: `acme.my-app.account-profile`

This is a root-level plugin in the `example.com/acme/my-app/v2` Go Module. Its declarative source is `plugin.yaml`, and its generated configuration adapter is committed under the module's `generated/go/configuration/` directory. Runnable applications generate the final selected-provider assembly centrally.

## Capabilities

This plugin starts without capabilities. Add a custom capability with:

```text
plystra capability create <capability-name>
```

Implement an existing canonical capability with:

```text
plystra capability implement <capability-name>/vN
```
