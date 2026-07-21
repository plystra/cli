import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { fromBinary, fromJson, toBinary, toJson } from "@bufbuild/protobuf";
import { ConnectError } from "@connectrpc/connect";
import {
  PlystraError,
  createCompatSendV1,
  createEmailSendV1,
  createMailDeliverV1,
  createPlystraClient,
} from "@acme/project-sdk";
import { resolveMessage, resolveUnaryMethod } from "../dist/descriptors.js";

const emailMethod = resolveUnaryMethod(
  "plystra.generated.email.send.v1.EmailSendV1Service",
  "Invoke",
  "plystra.generated.email.send.v1.EmailSendV1Request",
  "plystra.generated.email.send.v1.EmailSendV1Response",
);
const errorDetailDescriptor = resolveMessage(
  "plystra.generated.transport.v1.PlystraErrorDetail",
);

function methodFor(capabilityPackage, typeBase) {
  return resolveUnaryMethod(
    `${capabilityPackage}.${typeBase}Service`,
    "Invoke",
    `${capabilityPackage}.${typeBase}Request`,
    `${capabilityPackage}.${typeBase}Response`,
  );
}

function protobufResponse(method, json) {
  const message = fromJson(method.output, json, {
    ignoreUnknownFields: false,
  });
  return new Response(toBinary(method.output, message), {
    status: 200,
    headers: { "Content-Type": "application/proto" },
  });
}

function connectErrorResponse(code, message, status, details = []) {
  return new Response(JSON.stringify({ code, message, details }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function safeErrorDetail({
  requestedCapabilityID = "email.send/v1",
  canonicalCapabilityID = "email.send/v1",
  semanticErrorCode = "",
  kernelErrorClass = "",
  traceID = "",
} = {}) {
  const message = fromJson(
    errorDetailDescriptor,
    {
      requestedCapabilityId: requestedCapabilityID,
      canonicalCapabilityId: canonicalCapabilityID,
      semanticErrorCode,
      kernelErrorClass,
      traceId: traceID,
    },
    { ignoreUnknownFields: false },
  );
  return {
    type: errorDetailDescriptor.typeName,
    value: Buffer.from(toBinary(errorDetailDescriptor, message)).toString(
      "base64",
    ),
  };
}

test("package exports and declarations hide Connect internals", async () => {
  for (const specifier of [
    "@acme/project-sdk/runtime.js",
    "@acme/project-sdk/descriptors.js",
    "@acme/project-sdk/dist/runtime.js",
  ]) {
    await assert.rejects(
      import(specifier),
      (error) => error?.code === "ERR_PACKAGE_PATH_NOT_EXPORTED",
    );
  }

  const [runtimeDeclaration, descriptorDeclaration, operationDeclaration] =
    await Promise.all([
      readFile(new URL("../dist/runtime.d.ts", import.meta.url), "utf8"),
      readFile(new URL("../dist/descriptors.d.ts", import.meta.url), "utf8"),
      readFile(
        new URL("../dist/operations/email/send/v1.d.ts", import.meta.url),
        "utf8",
      ),
    ]);
  assert.doesNotMatch(
    runtimeDeclaration,
    /\b(?:ConnectError|Transport|Runtime|MessageCodec|createRuntime|invoke)\b/,
  );
  assert.doesNotMatch(
    descriptorDeclaration,
    /\b(?:DescMethodUnary|resolveUnaryMethod)\b/,
  );
  assert.doesNotMatch(
    operationDeclaration,
    /\bbindOperation(?:Method)?\b/,
  );
});

async function requestJSON(method, body) {
  const bytes = new Uint8Array(await new Response(body).arrayBuffer());
  return toJson(method.input, fromBinary(method.input, bytes), {
    alwaysEmitImplicit: true,
    enumAsInteger: false,
    useProtoFieldName: false,
  });
}

test("nested client sends binary Connect with one access token", async () => {
  const calls = [];
  const controller = new AbortController();
  const client = createPlystraClient({
    baseUrl: "https://api.example.test/root",
    getAccessToken: async () => "browser-token",
    fetch: async (input, init) => {
      calls.push({ input, init, signalAborted: init.signal?.aborted });
      return protobufResponse(emailMethod, {
        accepted: true,
        latency: 1.5,
        positions: {
          values: ["-9223372036854775808", "9223372036854775807"],
        },
        revision: "9223372036854775807",
      });
    },
  });

  const response = await client.email.send.v1(
    {
      to: "person@example.com",
      tags: ["welcome"],
      priority: "urgent",
      checkpoints: [
        -9_223_372_036_854_775_808n,
        9_223_372_036_854_775_807n,
      ],
      offset: -9_223_372_036_854_775_808n,
      retries: 9_223_372_036_854_775_807n,
      metadata: { nested: [true, null, { count: 1 }] },
    },
    { signal: controller.signal },
  );

  assert.deepEqual(response, {
    accepted: true,
    latency: 1.5,
    positions: [
      -9_223_372_036_854_775_808n,
      9_223_372_036_854_775_807n,
    ],
    revision: 9_223_372_036_854_775_807n,
  });
  assert.equal(calls.length, 1);
  const [{ input, init, signalAborted }] = calls;
  assert.equal(
    new URL(input).href,
    "https://api.example.test/root/plystra.generated.email.send.v1.EmailSendV1Service/Invoke",
  );
  assert.equal(init.method, "POST");
  assert.equal(init.cache, "no-store");
  assert.equal(init.credentials, "same-origin");
  assert.equal(init.redirect, "error");
  assert.equal(init.signal instanceof AbortSignal, true);
  assert.equal(signalAborted, false);
  const headers = new Headers(init.headers);
  assert.equal(headers.get("Content-Type"), "application/proto");
  assert.equal(headers.get("Connect-Protocol-Version"), "1");
  assert.equal(headers.get("Authorization"), "Bearer browser-token");
  const encoded = await requestJSON(emailMethod, init.body);
  assert.deepEqual(encoded.metadata, {
    nested: [true, null, { count: 1 }],
  });
  assert.deepEqual(encoded.tags, { values: ["welcome"] });
  assert.deepEqual(encoded.checkpoints, {
    values: ["-9223372036854775808", "9223372036854775807"],
  });
  assert.equal(encoded.offset, "-9223372036854775808");
  assert.equal(encoded.to, "person@example.com");
  assert.notEqual(encoded.priority, "urgent");
  assert.notEqual(encoded.retries, 9_223_372_036_854_775_807n);
  assert.equal(Object.isFrozen(client), true);
  assert.equal(Object.isFrozen(client.email), true);
  assert.equal(Object.isFrozen(client.email.send), true);
});

test("tree-shakable operation factory uses the same Connect descriptor", async () => {
  let requested = "";
  const send = createEmailSendV1({
    baseUrl: new URL("https://api.example.test/"),
    fetch: async (input) => {
      requested = new URL(input).href;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  assert.deepEqual(
    await send({ to: "person@example.com", tags: [], priority: "normal" }),
    { accepted: true },
  );
  assert.equal(
    requested,
    "https://api.example.test/plystra.generated.email.send.v1.EmailSendV1Service/Invoke",
  );
});

test("aliases reuse the canonical messages over their own Connect procedures", async () => {
  const paths = [];
  const options = {
    baseUrl: "https://api.example.test/root",
    fetch: async (input) => {
      paths.push(new URL(input).pathname);
      return protobufResponse(emailMethod, { accepted: true });
    },
  };
  const client = createPlystraClient(options);
  const request = {
    to: "person@example.com",
    tags: ["alias"],
    priority: "normal",
  };

  assert.deepEqual(await client.compat.send.v1(request), { accepted: true });
  assert.deepEqual(await client.mail.deliver.v1(request), { accepted: true });
  assert.deepEqual(await createCompatSendV1(options)(request), {
    accepted: true,
  });
  assert.deepEqual(await createMailDeliverV1(options)(request), {
    accepted: true,
  });
  assert.deepEqual(paths, [
    "/root/plystra.generated.compat.send.v1.CompatSendV1Service/Invoke",
    "/root/plystra.generated.mail.deliver.v1.MailDeliverV1Service/Invoke",
    "/root/plystra.generated.compat.send.v1.CompatSendV1Service/Invoke",
    "/root/plystra.generated.mail.deliver.v1.MailDeliverV1Service/Invoke",
  ]);

  await assert.rejects(() => client.compat.send.v1({}), TypeError);
  assert.equal(paths.length, 4);
});

test("request validation rejects malformed and oversized values before fetch", async () => {
  let calls = 0;
  const send = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  const cyclic = {};
  cyclic.self = cyclic;
  const invalid = [
    {},
    { to: "person@example.com", tags: [], priority: "later" },
    { to: "person@example.com", tags: [1], priority: "normal" },
    { to: "person@example.com", tags: [], priority: "normal", retries: 3 },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      metadata: new Date(),
    },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      metadata: cyclic,
    },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      unexpected: true,
    },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      offset: 1,
    },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      offset: 9_223_372_036_854_775_808n,
    },
  ];
  for (const request of invalid) {
    await assert.rejects(() => send(request), TypeError);
  }
  await assert.rejects(
    () =>
      send({
        to: "x".repeat(1_048_576),
        tags: [],
        priority: "normal",
      }),
    RangeError,
  );
  assert.equal(calls, 0);
});

test("response and Connect errors expose only Plystra-owned stable fields", async () => {
  const responses = [
    protobufResponse(emailMethod, {}),
    new Response(new Uint8Array([0xff]), {
      status: 200,
      headers: { "Content-Type": "application/proto" },
    }),
    new Response("{}", {
      status: 200,
      headers: { "Content-Type": "text/plain" },
    }),
  ];
  const send = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => responses.shift(),
  });
  for (let index = 0; index < 3; index++) {
    await assert.rejects(
      () => send({ to: "person@example.com", tags: [], priority: "normal" }),
      (error) =>
        error instanceof PlystraError &&
        !error.message.includes("protobuf") &&
        !error.message.includes("content-type"),
    );
  }

  const semantic = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () =>
      connectErrorResponse(
        "failed_precondition",
        "provider secret must not escape",
        400,
        [
          safeErrorDetail({
            semanticErrorCode: "temporarily_unavailable",
            traceID: "trace-123",
          }),
        ],
      ),
  });
  await assert.rejects(
    () => semantic({ to: "person@example.com", tags: [], priority: "normal" }),
    (error) => {
      assert.equal(error instanceof PlystraError, true);
      assert.equal(error.status, 422);
      assert.equal(error.code, "capability_error");
      assert.deepEqual(error.detail, {
        requestedCapabilityID: "email.send/v1",
        canonicalCapabilityID: "email.send/v1",
        semanticErrorCode: "temporarily_unavailable",
        traceID: "trace-123",
      });
      assert.equal(Object.isFrozen(error.detail), true);
      assert.equal(error.message, "capability_error");
      assert.equal(error instanceof Error && error.name, "PlystraError");
      assert.equal(error instanceof ConnectError, false);
      assert.equal("rawMessage" in error, false);
      assert.equal("details" in error, false);
      assert.equal("cause" in error, false);
      assert.equal("detailCode" in error, false);
      return true;
    },
  );

  const aliasSemantic = createMailDeliverV1({
    baseUrl: "https://api.example.test",
    fetch: async () =>
      connectErrorResponse("failed_precondition", "hidden", 400, [
        safeErrorDetail({
          requestedCapabilityID: "mail.deliver/v1",
          semanticErrorCode: "invalid_recipient",
        }),
      ]),
  });
  await assert.rejects(
    () =>
      aliasSemantic({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof PlystraError &&
      error.code === "capability_error" &&
      error.detail?.requestedCapabilityID === "mail.deliver/v1" &&
      error.detail?.canonicalCapabilityID === "email.send/v1" &&
      error.detail?.semanticErrorCode === "invalid_recipient",
  );

  const malformedError = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () =>
      connectErrorResponse("provider_secret", "unsafe detail", 500),
  });
  await assert.rejects(
    () =>
      malformedError({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof PlystraError &&
      !error.message.includes("provider_secret") &&
      !error.message.includes("unsafe detail"),
  );
});

test("closed Kernel error classes retain typed safe details", async () => {
  const cases = [
    ["invalid_argument", "invalid_argument", 400, "invalid_argument"],
    ["not_found", "not_found", 404, "not_found"],
    ["aborted", "conflict", 409, "conflict"],
    ["permission_denied", "denied", 403, "denied"],
    ["unauthenticated", "unauthenticated", 401, "unauthenticated"],
    ["unavailable", "unavailable", 503, "unavailable"],
    ["deadline_exceeded", "timeout", 504, "timeout"],
    ["canceled", "cancelled", 0, "cancelled"],
    ["unavailable", "result_unknown", 503, "result_unknown"],
    ["internal", "internal", 500, "internal"],
    ["unimplemented", "version_incompatible", 501, "version_incompatible"],
  ];
  for (const [connectCode, kernelErrorClass, status, publicCode] of cases) {
    const send = createEmailSendV1({
      baseUrl: "https://api.example.test",
      fetch: async () =>
        connectErrorResponse(connectCode, "provider secret", 400, [
          safeErrorDetail({ kernelErrorClass }),
        ]),
    });
    await assert.rejects(
      () =>
        send({
          to: "person@example.com",
          tags: [],
          priority: "normal",
        }),
      (error) => {
        assert.equal(error instanceof PlystraError, true);
        assert.equal(error.status, status);
        assert.equal(error.code, publicCode);
        assert.deepEqual(error.detail, {
          requestedCapabilityID: "email.send/v1",
          canonicalCapabilityID: "email.send/v1",
          kernelErrorClass,
        });
        assert.equal(error.message.includes("provider secret"), false);
        return true;
      },
    );
  }
});

test("missing, malformed, duplicate, and mismatched details fail closed", async () => {
  const validSemantic = safeErrorDetail({
    semanticErrorCode: "temporarily_unavailable",
  });
  const unknownType = { ...validSemantic, type: "unsafe.ProviderSecret" };
  const withUnknownField = {
    ...validSemantic,
    value: Buffer.concat([
      Buffer.from(validSemantic.value, "base64"),
      Buffer.from([0x32, 0x01, 0x78]),
    ]).toString("base64"),
  };
  const invalid = [
    { code: "failed_precondition", details: [] },
    {
      code: "failed_precondition",
      details: [validSemantic, validSemantic],
    },
    { code: "failed_precondition", details: [unknownType] },
    {
      code: "failed_precondition",
      details: [
        {
          type: errorDetailDescriptor.typeName,
          value: Buffer.from([0xff]).toString("base64"),
        },
      ],
    },
    {
      code: "failed_precondition",
      details: [
        safeErrorDetail({
          requestedCapabilityID: "mail.deliver/v1",
          semanticErrorCode: "temporarily_unavailable",
        }),
      ],
    },
    {
      code: "failed_precondition",
      details: [
        safeErrorDetail({
          canonicalCapabilityID: "other.capability/v1",
          semanticErrorCode: "temporarily_unavailable",
        }),
      ],
    },
    {
      code: "failed_precondition",
      details: [safeErrorDetail({ semanticErrorCode: "undeclared_secret" })],
    },
    {
      code: "failed_precondition",
      details: [
        safeErrorDetail({
          semanticErrorCode: "temporarily_unavailable",
          kernelErrorClass: "internal",
        }),
      ],
    },
    { code: "internal", details: [safeErrorDetail()] },
    {
      code: "internal",
      details: [safeErrorDetail({ kernelErrorClass: "provider_secret" })],
    },
    { code: "internal", details: [validSemantic] },
    {
      code: "failed_precondition",
      details: [validSemantic, unknownType],
    },
    { code: "failed_precondition", details: [withUnknownField] },
    {
      code: "failed_precondition",
      details: [
        safeErrorDetail({
          semanticErrorCode: "temporarily_unavailable",
          traceID: "unsafe trace secret",
        }),
      ],
    },
  ];
  for (const response of invalid) {
    const send = createEmailSendV1({
      baseUrl: "https://api.example.test",
      fetch: async () =>
        connectErrorResponse(
          response.code,
          "provider secret must not escape",
          400,
          response.details,
        ),
    });
    await assert.rejects(
      () =>
        send({
          to: "person@example.com",
          tags: [],
          priority: "normal",
        }),
      (error) =>
        error instanceof PlystraError &&
        error.status === 500 &&
        error.code === "internal" &&
        error.detail === undefined &&
        error.message === "internal" &&
        !error.message.includes("provider secret"),
    );
  }
});

test("credentials, cancellation, and network failures are bounded and normalized", async () => {
  let calls = 0;
  const invalidCredential = createEmailSendV1({
    baseUrl: "https://api.example.test",
    getAccessToken: () => " bad-token",
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  await assert.rejects(
    () =>
      invalidCredential({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    TypeError,
  );

  const prefixedCredential = createEmailSendV1({
    baseUrl: "https://api.example.test",
    getAccessToken: () => "Bearer browser-token",
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  await assert.rejects(
    () =>
      prefixedCredential({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof TypeError &&
      error.message ===
        "getAccessToken must return the raw token without the Bearer scheme",
  );

  const credentialFailure = createEmailSendV1({
    baseUrl: "https://api.example.test",
    getAccessToken: () => {
      throw new Error("credential secret");
    },
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  await assert.rejects(
    () =>
      credentialFailure({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof PlystraError &&
      error.code === "credential_error" &&
      error.message === "credential_error",
  );

  const controller = new AbortController();
  controller.abort();
  const cancelled = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  await assert.rejects(
    () =>
      cancelled(
        { to: "person@example.com", tags: [], priority: "normal" },
        { signal: controller.signal },
      ),
    (error) => error instanceof PlystraError && error.code === "cancelled",
  );

  const network = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      throw new Error("network detail");
    },
  });
  await assert.rejects(
    () => network({ to: "person@example.com", tags: [], priority: "normal" }),
    (error) =>
      error instanceof PlystraError &&
      error.code === "network_error" &&
      error.message === "network_error",
  );
  assert.equal(calls, 0);
});

test("empty contracts remain exact and base URLs reject unsafe components", async () => {
  assert.throws(
    () =>
      createPlystraClient({
        baseUrl: "https://user:password@example.test/?query=secret",
      }),
    TypeError,
  );
  const accountMethod = methodFor(
    "plystra.generated.account.profile.get.v2",
    "AccountProfileGetV2",
  );
  let calls = 0;
  const client = createPlystraClient({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      calls++;
      return protobufResponse(accountMethod, {});
    },
  });
  assert.deepEqual(await client.account.profile.get.v2({}), {});
  await assert.rejects(
    () => client.account.profile.get.v2({ unexpected: true }),
    TypeError,
  );
  assert.equal(calls, 1);
});

test("prefix and hyphenated Capability identities remain collision-free", async () => {
  const methods = new Map([
    [
      "/plystra.generated.alpha.beta.v1.AlphaBetaV1Service/Invoke",
      methodFor("plystra.generated.alpha.beta.v1", "AlphaBetaV1"),
    ],
    [
      "/plystra.generated.alpha.beta.v1.check.v1.AlphaBetaV1CheckV1Service/Invoke",
      methodFor(
        "plystra.generated.alpha.beta.v1.check.v1",
        "AlphaBetaV1CheckV1",
      ),
    ],
    [
      "/plystra.generated.foo_h_bar.send.v1.FooBarSendV1Service/Invoke",
      methodFor("plystra.generated.foo_h_bar.send.v1", "FooBarSendV1"),
    ],
    [
      "/plystra.generated.foo.bar_h_send.v1.FooBarSendV1Service/Invoke",
      methodFor("plystra.generated.foo.bar_h_send.v1", "FooBarSendV1"),
    ],
  ]);
  const paths = [];
  const client = createPlystraClient({
    baseUrl: "https://api.example.test",
    fetch: async (input) => {
      const path = new URL(input).pathname;
      paths.push(path);
      return protobufResponse(methods.get(path), {});
    },
  });
  await client.alpha.beta.v1({});
  await client.alpha.beta.v1.check.v1({});
  await client["foo-bar"].send.v1({});
  await client.foo["bar-send"].v1({});
  assert.deepEqual(paths, [...methods.keys()]);
});
