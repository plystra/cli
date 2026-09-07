# Account Profile plugin

Plugin ID: `acme.my-app.account-profile`

This is a root-level Plugin in the `example.com/acme/my-app/v2` Plystra Project. Its declarative source is `plugin.yaml`, and its generated configuration adapter is committed under the Project's `generated/go/configuration/` directory. Every Project generates the final selected-Provider assembly centrally.

## Capabilities

Canonical capabilities implemented by this plugin are listed under `provides` in `plugin.yaml`. Their declarations live at `capabilities/<capability-name>/vN/capability.yaml`, and provider methods remain in plugin-owned Go files outside `generated/`.

Create a custom capability from the module root with:

```text
plystra capability create <capability-name> --query --plugin account-profile
```

Implement an existing canonical capability with:

```text
plystra capability implement <capability-name>/vN --plugin account-profile
```

An already-visible exact create target uses
`PLYSTRA_CAPABILITY_CREATE_ALREADY_VISIBLE`; a non-visible exact
implement target uses `PLYSTRA_CAPABILITY_IMPLEMENT_NOT_VISIBLE`.
Follow the emitted counterpart command; neither failure changes the Project.
An explicit older or skipped new version uses
`PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED` and requires the
same create command to be repeated with `--confirm` after review.
An omitted version above the maximum visible major uses
`PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED` and requires a new
canonical Capability identity.
Creating a new identity without a profile uses
`PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED`; supplying
`--query` for a copied later version uses
`PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED`.
