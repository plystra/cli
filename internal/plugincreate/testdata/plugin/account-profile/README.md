# Account Profile plugin

Plugin ID: `acme.my-app.account-profile`

This is a root-level plugin in the `example.com/acme/my-app/v2` Go Module. Its declarative source is `plugin.yaml`, and its generated configuration adapter is committed under the module's `generated/go/configuration/` directory. Runnable applications generate the final selected-provider assembly centrally.

## Capabilities

Canonical capabilities implemented by this plugin are listed under `provides` in `plugin.yaml`. Their declarations live at `capabilities/<capability-name>/vN/capability.yaml`, and provider methods remain in plugin-owned Go files outside `generated/`.

Create a custom capability from the module root with:

```text
plystra capability create <capability-name> --plugin account-profile
```

Implement an existing canonical capability with:

```text
plystra capability implement <capability-name>/vN --plugin account-profile
```
