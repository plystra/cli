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

Generated application failures expose only an immutable Plystra-owned safe detail. On the wire, `requested_interface_id` records the requested canonical Interface or temporary pre-removal Alias, `canonical_interface_id` records the canonical Interface target, and exactly one declared semantic code or closed Kernel class is present. Implementation text, causes, payloads, panic data, configuration, credentials, Secrets, internal Kernel detail codes, and raw Connect details are excluded. Missing, duplicate, malformed, unknown, mismatched, or undeclared details fail closed to `internal`; inspect `PlystraError.detail` rather than parsing an error message.

Transitional legacy `integer` fields and integer array items are signed 64-bit values exposed as JavaScript `bigint`, including enum literals such as `0n`. Pass `bigint`, not `number`, so request and response values remain exact across the full range.

Canonical Interface types preserve exact authored widths: `int32` and `uint32` are JavaScript `number`; `int64` and `uint64` are `bigint`; `float32` and `float64` are `number`; bytes are `Uint8Array`; timestamps are RFC 3339 strings; durations are Protobuf JSON duration strings; repeated values are readonly arrays; and maps are readonly string-keyed records. Boolean and integer Go map keys use their canonical ProtoJSON string form.

Every property uses the Interface field's effective JSON name. Authored required markers produce required readonly properties; every other field remains optional. Nested same-package messages are exported from the package root together with the canonical request and response types.

## Exposed Interface types

- `records.echo/v1` (`sha256:9edac981e07d60dff81938719b99315face2aa89e8111426f76030fc1de6fe4d`)

Transitional legacy request and response declarations retain each exact normalized constraint object in a `@plystraConstraints` field annotation. The wrapper preflights Unicode scalar-value length, numeric bounds, and array item counts before sending a request and applies the same portable checks to decoded responses. Canonical `pattern` uses Go regular-expression semantics, so it is declared for tools and developers but remains enforced authoritatively by the generated server rather than reinterpreted through JavaScript `RegExp`.

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

Pass an `AbortSignal` as the operation's second argument to cancel before dispatch or while the request is in flight. Cancellation rejects with `PlystraError` code `cancelled`; once server invocation has begun, it reaches the generated Connect handler, canonical invocation, and Provider context. Cancellation is best-effort interruption and does not promise Provider rollback.

Only explicitly exposed canonical operations and validated application-local Alias surfaces are generated. Alias methods reuse their direct canonical target contract and invoke the matching generated Alias Connect procedure. Provider packages, server configuration, verified internal context, and Secret values are never included.

## Canonical operations

- `account.profile.get/v2` (`sha256:96f322e70226b5392d4c68c0ad1c58c0a3d654110e225ac50623ebc81e10d19b`)
- `alpha.beta.v1.check/v1` (`sha256:3d25fdeab0811a282920ee03f2eafa5d4ccb3b3de39b69a85e3f93edc4dbea85`)
- `alpha.beta/v1` (`sha256:e4f1a8e37e47c8ff3bf76863c2ecbf470a56bb590a77a1ee9708330c452c7acd`)
- `email.send/v1` (`sha256:d1bb3e79da4ce8fc729c4a21d4ebabd3818436c1aac31d407d68ae96b8319e26`)
- `foo-bar.send/v1` (`sha256:0de2dab1912e93c308a77befb2441b21c92133597154902e71b520ee23237db2`)
- `foo.bar-send/v1` (`sha256:289854b61ff7acc97547f7b425670e742bc93f32169c80904618b4da4d1b8e3a`)

## Capability aliases

- `compat.send/v1` -> `email.send/v1` (`sha256:d1bb3e79da4ce8fc729c4a21d4ebabd3818436c1aac31d407d68ae96b8319e26`)
- `mail.deliver/v1` -> `email.send/v1` (`sha256:d1bb3e79da4ce8fc729c4a21d4ebabd3818436c1aac31d407d68ae96b8319e26`) - deprecated: "Use email.send/v1 instead."
