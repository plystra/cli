# @acme/project-sdk

Generated Plystra application SDK. Do not edit generated files.

## Validate

```sh
npm install --ignore-scripts --no-audit --no-fund
npm run typecheck
npm run build
npm pack --dry-run --json
```

The generated `.npmrc` disables lockfile creation because this package is CLI-owned. Installation may create only the ignored `node_modules/` and `dist/` validation outputs.

The Plystra wrapper resolves generated Protobuf descriptors and sends binary Connect requests through its pinned `@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web` dependencies. Application code does not construct raw Protobuf messages or Connect clients, and raw Connect errors are normalized before they cross the wrapper boundary. Import only the package root; the export map blocks internal subpaths and generated declarations omit transport, descriptor, codec, and binder internals.

Generated application failures expose only an immutable Plystra-owned safe detail containing the requested canonical or Alias Capability ID, its canonical target, and exactly one declared semantic code or closed Kernel class. Provider text, causes, payloads, panic data, configuration, credentials, Secrets, and raw Connect details are excluded. Missing, duplicate, malformed, unknown, mismatched, or undeclared details fail closed to `internal`; inspect `PlystraError.detail` rather than parsing an error message.

Canonical `integer` fields and integer array items are signed 64-bit values exposed as JavaScript `bigint`, including enum literals such as `0n`. Pass `bigint`, not `number`, so request and response values remain exact across the full range.

## Usage

```ts
import { createPlystraClient } from "@acme/project-sdk";

const client = createPlystraClient({
  baseUrl: "https://api.example.com",
  getAccessToken: async () => rawAccessToken,
});

const response = await client.account.profile.get.v2({});
```

`getAccessToken` returns only the raw token value. The generated transport adds the `Bearer` authorization scheme; returning a value that already includes that scheme fails before the request is sent.

Only explicitly exposed canonical operations and validated application-local Alias surfaces are generated. Alias methods reuse their direct canonical target contract and invoke the matching generated Alias Connect procedure. Provider packages, server configuration, verified internal context, and Secret values are never included.

## Canonical operations

- `account.profile.get/v2` (`sha256:96f322e70226b5392d4c68c0ad1c58c0a3d654110e225ac50623ebc81e10d19b`)
- `alpha.beta.v1.check/v1` (`sha256:3d25fdeab0811a282920ee03f2eafa5d4ccb3b3de39b69a85e3f93edc4dbea85`)
- `alpha.beta/v1` (`sha256:e4f1a8e37e47c8ff3bf76863c2ecbf470a56bb590a77a1ee9708330c452c7acd`)
- `email.send/v1` (`sha256:f8a801ed064ea942c0a6999e45359fba607be8d62fabce6d6ebec8909a5b3833`)
- `foo-bar.send/v1` (`sha256:0de2dab1912e93c308a77befb2441b21c92133597154902e71b520ee23237db2`)
- `foo.bar-send/v1` (`sha256:289854b61ff7acc97547f7b425670e742bc93f32169c80904618b4da4d1b8e3a`)

## Capability aliases

- `compat.send/v1` -> `email.send/v1` (`sha256:f8a801ed064ea942c0a6999e45359fba607be8d62fabce6d6ebec8909a5b3833`)
- `mail.deliver/v1` -> `email.send/v1` (`sha256:f8a801ed064ea942c0a6999e45359fba607be8d62fabce6d6ebec8909a5b3833`) - deprecated: "Use email.send/v1 instead."
