# @acme/project-sdk

Generated Plystra application SDK. Do not edit generated files.

## Usage

```ts
import { createPlystraClient } from "@acme/project-sdk";

const client = createPlystraClient({
  baseUrl: "https://api.example.com",
  getAccessToken: async () => accessToken,
});

const response = await client.account.profile.get.v2({});
```

Only explicitly exposed canonical operations and validated application-local Alias surfaces are generated. Alias methods reuse their direct canonical target contract and invoke the matching generated Alias HTTP route. Provider packages, server configuration, verified internal context, and Secret values are never included.

## Canonical operations

- `account.profile.get/v2` (`sha256:b867859b40c593d2fb90992083d56c12b97691e7507b69e117c3de5671f0d036`)
- `alpha.beta.v1.check/v1` (`sha256:a24f358a583668f8ea4e8f1ec0493681689946d7ca8bac52d95ab1cb98ef9b9e`)
- `alpha.beta/v1` (`sha256:570f299106e1a66a1c0d760720a54cbcf9054b763f38bc41ba65fe89e4dc1f26`)
- `email.send/v1` (`sha256:836377411dbd56a4b2e7441377ce8e3f093d1e8758677ae44b8062bb69c6a8a5`)
- `foo-bar.send/v1` (`sha256:29cf8dc8817a0f702540369a08146a329f4f1d482310112b79871d821d8b9d35`)
- `foo.bar-send/v1` (`sha256:0203205b8651a604925db9af60394d2f3272bd9d5cb969bb87cc6c5594831c4e`)

## Capability aliases

- `compat.send/v1` -> `email.send/v1` (`sha256:836377411dbd56a4b2e7441377ce8e3f093d1e8758677ae44b8062bb69c6a8a5`)
- `mail.deliver/v1` -> `email.send/v1` (`sha256:836377411dbd56a4b2e7441377ce8e3f093d1e8758677ae44b8062bb69c6a8a5`) - deprecated: "Use email.send/v1 instead."
