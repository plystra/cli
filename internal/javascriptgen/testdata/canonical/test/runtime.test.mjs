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
  createRecordsEchoV1,
} from "@acme/project-sdk";
import { resolveMessage, resolveUnaryMethod } from "../dist/descriptors.js";

const emailMethod = resolveUnaryMethod(
  "plystra.generated.email.send.v1.EmailSendV1Service",
  "Invoke",
  "plystra.generated.email.send.v1.EmailSendV1Request",
  "plystra.generated.email.send.v1.EmailSendV1Response",
);
const recordsMethod = resolveUnaryMethod(
  "plystra.generated.records.echo.v1.RecordsEchoV1Service",
  "Invoke",
  "plystra.generated.records.echo.v1.RecordsEchoV1Request",
  "plystra.generated.records.echo.v1.RecordsEchoV1Response",
);
const errorDetailDescriptor = resolveMessage(
  "plystra.generated.transport.v1.PlystraErrorDetail",
);
const anonymousCredentialPolicy = Object.freeze({ mode: "anonymous" });

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
      requestedInterfaceId: requestedCapabilityID,
      canonicalInterfaceId: canonicalCapabilityID,
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

function recordsEnvelope(overrides = {}) {
  return {
    active: true,
    count_32: -2_147_483_648,
    count_64: -9_223_372_036_854_775_808n,
    unsigned_32: 4_294_967_295,
    unsigned_64: 18_446_744_073_709_551_615n,
    ratio_32: 1.25,
    ratio_64: -9.5,
    name: "canonical",
    payload: new Uint8Array([0, 255, 1]),
    tags: ["one", "two"],
    scores: {
      low: -9_223_372_036_854_775_808n,
      high: 9_223_372_036_854_775_807n,
    },
    detail: {
      code: "root",
      amount: -1n,
      children: [{ code: "leaf", children: [] }],
    },
    items: [{
      code: "item",
      amount: 9_223_372_036_854_775_807n,
      children: [],
    }],
    lookup: { nested: { code: "lookup", amount: 2n, children: [] } },
    at: "2026-07-25T01:02:03.456Z",
    delay: "1.500s",
    payloads: [new Uint8Array([2, 3]), new Uint8Array()],
    identifiers: {
      "-9223372036854775808": 0n,
      "9223372036854775807": 18_446_744_073_709_551_615n,
    },
    DefaultName: "authored-json-name",
    ...overrides,
  };
}

function recordsEnvelopeJSON(overrides = {}) {
  return {
    active: true,
    count_32: -2_147_483_648,
    count_64: "-9223372036854775808",
    unsigned_32: 4_294_967_295,
    unsigned_64: "18446744073709551615",
    ratio_32: 1.25,
    ratio_64: -9.5,
    name: "canonical",
    payload: "AP8B",
    tags: ["one", "two"],
    scores: {
      low: "-9223372036854775808",
      high: "9223372036854775807",
    },
    detail: {
      code: "root",
      amount: "-1",
      children: [{ code: "leaf", children: [] }],
    },
    items: [{
      code: "item",
      amount: "9223372036854775807",
      children: [],
    }],
    lookup: { nested: { code: "lookup", amount: "2", children: [] } },
    at: "2026-07-25T01:02:03.456Z",
    delay: "1.500s",
    payloads: ["AgM=", ""],
    identifiers: {
      "-9223372036854775808": "0",
      "9223372036854775807": "18446744073709551615",
    },
    DefaultName: "authored-json-name",
    ...overrides,
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

  const [
    runtimeDeclaration,
    descriptorDeclaration,
    operationDeclaration,
    interfaceDeclaration,
  ] =
    await Promise.all([
      readFile(new URL("../dist/runtime.d.ts", import.meta.url), "utf8"),
      readFile(new URL("../dist/descriptors.d.ts", import.meta.url), "utf8"),
      readFile(
        new URL("../dist/operations/email/send/v1.d.ts", import.meta.url),
        "utf8",
      ),
      readFile(
        new URL("../dist/interfaces/records/echo/v1.d.ts", import.meta.url),
        "utf8",
      ),
    ]);
  assert.doesNotMatch(
    runtimeDeclaration,
    /\b(?:ConnectError|Transport|Runtime|MessageCodec|InterfaceCodec|InterfaceValueCodec|createRuntime|invoke|invokeInterface)\b/,
  );
  assert.doesNotMatch(
    descriptorDeclaration,
    /\b(?:DescMethodUnary|resolveUnaryMethod)\b/,
  );
  assert.doesNotMatch(
    operationDeclaration,
    /\bbindOperation(?:Method)?\b/,
  );
  assert.doesNotMatch(
    interfaceDeclaration,
    /\b(?:bindOperation|invokeInterface|InterfaceCodec|Runtime|resolveMessage|resolveUnaryMethod)\b/,
  );
  assert.match(
    interfaceDeclaration,
    /export declare function createOperation\(options: ClientOptions\): Operation;/,
  );
  assert.match(
    operationDeclaration,
    /@plystraConstraints \{"min_length":2,"max_length":254,"pattern":"\^\[\^@\]\+@\[\^@\]\+\$"\}/,
  );
  assert.doesNotMatch(
    runtimeDeclaration,
    /\bisStringWithinUnicodeScalarBounds\b/,
  );
});

test("canonical Interface method preserves every authored field over Connect", async () => {
  const paths = [];
  const requests = [];
  const expectedRequest = recordsEnvelopeJSON();
  const expectedResponse = recordsEnvelope();
  const options = {
    baseUrl: "https://api.example.test/root",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async (input, init) => {
      paths.push(new URL(input).pathname);
      requests.push(await requestJSON(recordsMethod, init.body));
      return protobufResponse(recordsMethod, { value: expectedRequest });
    },
  };
  const client = createPlystraClient(options);

  assert.deepEqual(
    await client.records.echo.v1({ value: recordsEnvelope() }),
    { value: expectedResponse },
  );
  assert.deepEqual(
    await createRecordsEchoV1(options)({ value: recordsEnvelope() }),
    { value: expectedResponse },
  );
  assert.deepEqual(paths, [
    "/root/plystra.generated.records.echo.v1.RecordsEchoV1Service/Invoke",
    "/root/plystra.generated.records.echo.v1.RecordsEchoV1Service/Invoke",
  ]);
  assert.deepEqual(requests, [
    { value: expectedRequest },
    { value: expectedRequest },
  ]);
  for (const response of [
    await client.records.echo.v1({ value: recordsEnvelope() }),
  ]) {
    assert.equal(Object.getPrototypeOf(response), Object.prototype);
    assert.equal(Object.getPrototypeOf(response.value), Object.prototype);
    assert.equal(
      Object.getPrototypeOf(response.value.detail),
      Object.prototype,
    );
    assert.equal(Object.getPrototypeOf(response.value.scores), Object.prototype);
    assert.equal(Object.getPrototypeOf(response.value.lookup), Object.prototype);
  }
  assert.equal(Object.isFrozen(client.records), true);
  assert.equal(Object.isFrozen(client.records.echo), true);
});

test("canonical Interface method safely preserves dynamic object-property keys", async () => {
  const lookup = {};
  Object.defineProperty(lookup, "constructor", {
    value: { code: "constructor", children: [] },
    writable: true,
    enumerable: true,
    configurable: true,
  });
  Object.defineProperty(lookup, "prototype", {
    value: { code: "prototype", children: [] },
    writable: true,
    enumerable: true,
    configurable: true,
  });
  const protobufLookup = {
    constructor: { code: "constructor", children: [] },
    prototype: { code: "prototype", children: [] },
  };
  let encoded;
  const echo = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async (_input, init) => {
      encoded = await requestJSON(recordsMethod, init.body);
      return protobufResponse(recordsMethod, {
        value: recordsEnvelopeJSON({ lookup: protobufLookup }),
      });
    },
  });

  const response = await echo({
    value: recordsEnvelope({ lookup }),
  });
  assert.equal(
    Object.prototype.hasOwnProperty.call(encoded.value.lookup, "constructor"),
    true,
  );
  assert.equal(
    Object.prototype.hasOwnProperty.call(response.value.lookup, "constructor"),
    true,
  );
  assert.equal(
    Object.prototype.hasOwnProperty.call(response.value.lookup, "prototype"),
    true,
  );
  assert.deepEqual(response.value.lookup.constructor, {
    code: "constructor",
    children: [],
  });
  assert.deepEqual(response.value.lookup.prototype, {
    code: "prototype",
    children: [],
  });
  assert.equal(Object.getPrototypeOf(response.value.lookup), Object.prototype);
});

test("canonical Interface request validation rejects invalid shapes and widths before fetch", async () => {
  let calls = 0;
  const echo = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () => {
      calls++;
      return protobufResponse(recordsMethod, { value: recordsEnvelopeJSON() });
    },
  });
  const request = (overrides) => ({
    value: recordsEnvelope(overrides),
  });
  const invalid = [
    {},
    { value: {} },
    { value: recordsEnvelope(), unexpected: true },
    request({ unexpected: true }),
    request({ active: "true" }),
    request({ count_32: -2_147_483_649 }),
    request({ count_32: 2_147_483_648 }),
    request({ count_32: 1.5 }),
    request({ count_64: 1 }),
    request({ count_64: -9_223_372_036_854_775_809n }),
    request({ count_64: 9_223_372_036_854_775_808n }),
    request({ unsigned_32: -1 }),
    request({ unsigned_32: 4_294_967_296 }),
    request({ unsigned_64: -1n }),
    request({ unsigned_64: 18_446_744_073_709_551_616n }),
    request({ ratio_32: Number.NaN }),
    request({ ratio_32: 3.5e38 }),
    request({ ratio_64: Number.POSITIVE_INFINITY }),
    request({ name: 1 }),
    request({ payload: "AP8B" }),
    request({ tags: {} }),
    request({ tags: ["valid", 1] }),
    request({ scores: [] }),
    request({ scores: { invalid: 1 } }),
    request({ detail: [] }),
    request({ detail: { amount: 1n } }),
    request({ detail: { code: "nested", children: {} } }),
    request({ items: {} }),
    request({ items: [{ code: "valid", unexpected: true }] }),
    request({ lookup: [] }),
    request({ lookup: { nested: { amount: 1n } } }),
    request({ at: "not-a-timestamp" }),
    request({ delay: "not-a-duration" }),
    request({ payloads: ["AgM="] }),
    request({ identifiers: { "-0": 1n } }),
    request({ identifiers: { "01": 1n } }),
    request({ identifiers: { "+1": 1n } }),
    request({ identifiers: { "-9223372036854775809": 1n } }),
    request({ identifiers: { "9223372036854775808": 1n } }),
    request({ identifiers: { "1": -1n } }),
    request({ identifiers: { "1": 18_446_744_073_709_551_616n } }),
    request({ DefaultName: 1 }),
  ];
  const unsafeStringMap = {};
  Object.defineProperty(unsafeStringMap, "__proto__", {
    value: 1n,
    enumerable: true,
  });
  invalid.push(request({ scores: unsafeStringMap }));
  for (const value of invalid) {
    await assert.rejects(() => echo(value), TypeError);
  }

  const cyclic = { code: "cycle" };
  cyclic.children = [cyclic];
  await assert.rejects(
    () => echo(request({ detail: cyclic })),
    TypeError,
  );

  let deep = { code: "leaf" };
  for (let index = 0; index < 40; index++) {
    deep = { code: `depth-${index}`, children: [deep] };
  }
  await assert.rejects(
    () => echo(request({ detail: deep })),
    (error) =>
      error instanceof RangeError &&
      error.message === "Interface value exceeds the maximum nesting depth",
  );
  await assert.rejects(
    () => echo(request({ tags: Array(65_536).fill("") })),
    (error) =>
      error instanceof RangeError &&
      error.message === "Interface value exceeds the maximum node count",
  );
  await assert.rejects(
    () => echo(request({ payload: new Uint8Array(1_048_576) })),
    (error) =>
      error instanceof RangeError &&
      error.message === "request exceeds the 1 MiB transport limit",
  );
  assert.equal(calls, 0);
});

test("canonical Interface responses and semantic failures remain safe", async () => {
  const responses = [
    protobufResponse(recordsMethod, {}),
    protobufResponse(recordsMethod, {
      value: recordsEnvelopeJSON({ detail: { amount: "1" } }),
    }),
  ];
  const invalidResponse = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () => responses.shift(),
  });
  for (let index = 0; index < 2; index++) {
    await assert.rejects(
      () => invalidResponse({ value: recordsEnvelope() }),
      (error) =>
        error instanceof PlystraError &&
        error.code === "invalid_response" &&
        error.message === "invalid_response",
    );
  }

  const semantic = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () =>
      connectErrorResponse("failed_precondition", "implementation secret", 400, [
        safeErrorDetail({
          requestedCapabilityID: "records.echo/v1",
          canonicalCapabilityID: "records.echo/v1",
          semanticErrorCode: "record_rejected",
          traceID: "trace-records",
        }),
      ]),
  });
  await assert.rejects(
    () => semantic({ value: recordsEnvelope() }),
    (error) =>
      error instanceof PlystraError &&
      error.status === 422 &&
      error.code === "capability_error" &&
      error.message === "capability_error" &&
      error.detail?.requestedCapabilityID === "records.echo/v1" &&
      error.detail?.canonicalCapabilityID === "records.echo/v1" &&
      error.detail?.semanticErrorCode === "record_rejected" &&
      error.detail?.traceID === "trace-records" &&
      !error.message.includes("implementation secret"),
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

test("nested client sends binary Connect with one bearer credential", async () => {
  const calls = [];
  const controller = new AbortController();
  const client = createPlystraClient({
    baseUrl: "https://api.example.test/root",
    credentialPolicy: {
      mode: "bearer",
      getAccessToken: async () => "browser-token",
    },
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
  assert.equal(init.credentials, "omit");
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () => {
      calls++;
      return protobufResponse(emailMethod, { accepted: true });
    },
  });
  const cyclic = {};
  cyclic.self = cyclic;
  const invalid = [
    {},
    { to: "😀", tags: [], priority: "normal" },
    { to: "x".repeat(255), tags: [], priority: "normal" },
    { to: "person@example.com", tags: [], priority: "normal", label: "e\u0301" },
    { to: "person@example.com", tags: [], priority: "normal", label: "\ud800" },
    { to: "person@example.com", tags: [], priority: "later" },
    { to: "person@example.com", tags: [1], priority: "normal" },
    { to: "person@example.com", tags: ["one", "two", "three"], priority: "normal" },
    { to: "person@example.com", tags: [], priority: "normal", checkpoints: [] },
    {
      to: "person@example.com",
      tags: [],
      priority: "normal",
      checkpoints: [1n, 2n, 3n],
    },
    { to: "person@example.com", tags: [], priority: "normal", attempt: 0n },
    { to: "person@example.com", tags: [], priority: "normal", attempt: 4n },
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
        to: "person@example.com",
        tags: [],
        priority: "normal",
        metadata: { payload: "x".repeat(1_048_576) },
      }),
    RangeError,
  );
  assert.equal(calls, 0);
  assert.deepEqual(
    await send({
      to: "person@example.com",
      tags: [],
      priority: "normal",
      label: "😀",
      checkpoints: [1n],
      attempt: 3n,
      offset: 9_223_372_036_854_775_807n,
    }),
    { accepted: true },
  );
  assert.equal(calls, 1);
});

test("response validation enforces canonical field constraints", async () => {
  const responses = [
    { accepted: true, latency: 0.25 },
    { accepted: true, latency: 2 },
    { accepted: true, positions: { values: [] } },
    { accepted: true, positions: { values: ["1", "2", "3"] } },
    { accepted: true, attempt: "0" },
    { accepted: true, attempt: "4" },
  ];
  const send = createEmailSendV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () => protobufResponse(emailMethod, responses.shift()),
  });
  for (let index = 0; index < 6; index++) {
    await assert.rejects(
      () => send({ to: "person@example.com", tags: [], priority: "normal" }),
      (error) =>
        error instanceof PlystraError &&
        error.code === "invalid_response" &&
        error.message === "invalid_response",
    );
  }
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
      credentialPolicy: anonymousCredentialPolicy,
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
      credentialPolicy: anonymousCredentialPolicy,
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

test("credential policies select exact Fetch behavior without fallback", async () => {
  let bearerCalls = 0;
  const cases = [
    {
      name: "anonymous",
      credentialPolicy: { mode: "anonymous" },
      fetchCredentials: "omit",
      authorization: null,
    },
    {
      name: "cookie same-origin",
      credentialPolicy: {
        mode: "cookie",
        fetchCredentials: "same-origin",
      },
      fetchCredentials: "same-origin",
      authorization: null,
    },
    {
      name: "cookie include",
      credentialPolicy: {
        mode: "cookie",
        fetchCredentials: "include",
      },
      fetchCredentials: "include",
      authorization: null,
    },
    {
      name: "bearer",
      credentialPolicy: {
        mode: "bearer",
        getAccessToken: async () => {
          bearerCalls++;
          return "browser-token";
        },
      },
      fetchCredentials: "omit",
      authorization: "Bearer browser-token",
    },
  ];

  for (const policyCase of cases) {
    let observed;
    const client = createRecordsEchoV1({
      baseUrl: "https://api.example.test",
      credentialPolicy: policyCase.credentialPolicy,
      fetch: async (_input, init) => {
        observed = init;
        return protobufResponse(recordsMethod, {
          value: recordsEnvelopeJSON(),
        });
      },
    });
    await client({ value: recordsEnvelope() });

    assert.equal(observed.credentials, policyCase.fetchCredentials, policyCase.name);
    assert.equal(
      new Headers(observed.headers).get("Authorization"),
      policyCase.authorization,
      policyCase.name,
    );
  }
  assert.equal(bearerCalls, 1);
});

test("credential policy is required and malformed policies fail at construction", () => {
  const baseUrl = "https://api.example.test";
  const invalidOptions = [
    null,
    { baseUrl },
    { baseUrl, credentialPolicy: null },
    { baseUrl, credentialPolicy: [] },
    { baseUrl, credentialPolicy: {} },
    { baseUrl, credentialPolicy: { mode: "unknown" } },
    {
      baseUrl,
      credentialPolicy: { mode: "anonymous", getAccessToken: () => "token" },
    },
    { baseUrl, credentialPolicy: { mode: "cookie" } },
    {
      baseUrl,
      credentialPolicy: { mode: "cookie", fetchCredentials: "omit" },
    },
    { baseUrl, credentialPolicy: { mode: "bearer" } },
    {
      baseUrl,
      credentialPolicy: { mode: "bearer", getAccessToken: "token" },
    },
    {
      baseUrl,
      credentialPolicy: anonymousCredentialPolicy,
      getAccessToken: () => "token",
    },
    {
      baseUrl,
      credentialPolicy: anonymousCredentialPolicy,
      unexpected: true,
    },
  ];

  for (const options of invalidOptions) {
    assert.throws(() => createRecordsEchoV1(options), TypeError);
  }
});

test("bearer results are bounded, validated, and normalized without dispatch", async () => {
  const invalidResults = [
    null,
    undefined,
    "",
    "Bearer browser-token",
    "bearer",
    " browser-token",
    "browser-token ",
    "browser\ttoken",
    "browser\ntoken",
    "browser:token",
    42,
    { token: "browser-token" },
    "a".repeat(65_537),
  ];
  let fetchCalls = 0;

  for (const result of invalidResults) {
    const client = createRecordsEchoV1({
      baseUrl: "https://api.example.test",
      credentialPolicy: {
        mode: "bearer",
        getAccessToken: () => result,
      },
      fetch: async () => {
        fetchCalls++;
        return protobufResponse(recordsMethod, {
          value: recordsEnvelopeJSON(),
        });
      },
    });
    await assert.rejects(
      () => client({ value: recordsEnvelope() }),
      (error) =>
        error instanceof PlystraError &&
        error.status === 0 &&
        error.code === "credential_error" &&
        error.message === "credential_error" &&
        error.detail === undefined &&
        !("cause" in error),
    );
  }
  assert.equal(fetchCalls, 0);
});

test("bearer callback failures do not expose credential details", async () => {
  const secret = "credential secret must not escape";
  let fetchCalls = 0;
  for (const getAccessToken of [
    () => {
      throw new Error(secret);
    },
    async () => {
      throw new Error(secret);
    },
    () => {
      throw new PlystraError(0, "cancelled");
    },
  ]) {
    const client = createRecordsEchoV1({
      baseUrl: "https://api.example.test",
      credentialPolicy: { mode: "bearer", getAccessToken },
      fetch: async () => {
        fetchCalls++;
        return protobufResponse(recordsMethod, {
          value: recordsEnvelopeJSON(),
        });
      },
    });
    await assert.rejects(
      () => client({ value: recordsEnvelope() }),
      (error) =>
        error instanceof PlystraError &&
        error.code === "credential_error" &&
        error.message === "credential_error" &&
        !String(error).includes(secret) &&
        !JSON.stringify(error).includes(secret) &&
        !("cause" in error),
    );
  }
  assert.equal(fetchCalls, 0);
});

test("AbortSignal cancels before and during bearer token acquisition", async () => {
  let tokenCalls = 0;
  let fetchCalls = 0;
  const preCancelled = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: {
      mode: "bearer",
      getAccessToken: () => {
        tokenCalls++;
        return "browser-token";
      },
    },
    fetch: async () => {
      fetchCalls++;
      return protobufResponse(recordsMethod, {
        value: recordsEnvelopeJSON(),
      });
    },
  });
  const beforeController = new AbortController();
  beforeController.abort();
  await assert.rejects(
    () =>
      preCancelled(
        { value: recordsEnvelope() },
        { signal: beforeController.signal },
      ),
    (error) =>
      error instanceof PlystraError &&
      error.code === "cancelled" &&
      error.message === "cancelled",
  );
  assert.equal(tokenCalls, 0);
  assert.equal(fetchCalls, 0);

  let resolveStarted;
  let resolveToken;
  const started = new Promise((resolve) => {
    resolveStarted = resolve;
  });
  const token = new Promise((resolve) => {
    resolveToken = resolve;
  });
  const pendingToken = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: {
      mode: "bearer",
      getAccessToken: () => {
        tokenCalls++;
        resolveStarted();
        return token;
      },
    },
    fetch: async () => {
      fetchCalls++;
      return protobufResponse(recordsMethod, {
        value: recordsEnvelopeJSON(),
      });
    },
  });
  const duringController = new AbortController();
  const pending = pendingToken(
    { value: recordsEnvelope() },
    { signal: duringController.signal },
  );
  await Promise.race([
    started,
    new Promise((_resolve, reject) => {
      const timeout = setTimeout(
        () => reject(new Error("token callback did not start")),
        5_000,
      );
      started.then(() => clearTimeout(timeout));
    }),
  ]);
  duringController.abort();
  await assert.rejects(
    () => pending,
    (error) =>
      error instanceof PlystraError &&
      error.code === "cancelled" &&
      error.message === "cancelled",
  );
  resolveToken("late-browser-token");
  await Promise.resolve();
  assert.equal(tokenCalls, 1);
  assert.equal(fetchCalls, 0);
});

test("malformed request options and network failures remain safe", async () => {
  let calls = 0;
  const client = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async () => {
      calls++;
      throw new Error("network detail");
    },
  });
  for (const options of [null, { signal: null }, { unexpected: true }]) {
    await assert.rejects(
      () => client({ value: recordsEnvelope() }, options),
      TypeError,
    );
  }
  assert.equal(calls, 0);
  await assert.rejects(
    () => client({ value: recordsEnvelope() }),
    (error) =>
      error instanceof PlystraError &&
      error.code === "network_error" &&
      error.message === "network_error" &&
      !error.message.includes("network detail"),
  );
  assert.equal(calls, 1);
});

test("in-flight AbortSignal cancellation reaches the canonical Interface transport", async () => {
  const controller = new AbortController();
  let calls = 0;
  let resolveStarted;
  let observedAbort = false;
  const started = new Promise((resolve) => {
    resolveStarted = resolve;
  });
  const client = createRecordsEchoV1({
    baseUrl: "https://api.example.test",
    credentialPolicy: anonymousCredentialPolicy,
    fetch: async (_input, init) => {
      calls++;
      const signal = init.signal;
      resolveStarted();
      return await new Promise((_resolve, reject) => {
        const timeout = setTimeout(
          () => reject(new Error("caller cancellation did not reach fetch")),
          5_000,
        );
        const abort = () => {
          clearTimeout(timeout);
          observedAbort = true;
          reject(signal?.reason ?? new DOMException("aborted", "AbortError"));
        };
        if (signal?.aborted === true) {
          abort();
          return;
        }
        signal?.addEventListener("abort", abort, { once: true });
      });
    },
  });

  const pending = client(
    { value: recordsEnvelope() },
    { signal: controller.signal },
  );
  await Promise.race([
    started,
    new Promise((_resolve, reject) => {
      const timeout = setTimeout(
        () => reject(new Error("request did not reach fetch")),
        5_000,
      );
      started.then(() => clearTimeout(timeout));
    }),
  ]);
  controller.abort();

  await assert.rejects(
    () => pending,
    (error) =>
      error instanceof PlystraError &&
      error.code === "cancelled" &&
      error.message === "cancelled",
  );
  assert.equal(calls, 1);
  assert.equal(observedAbort, true);
});

test("empty contracts remain exact and base URLs reject unsafe components", async () => {
  assert.throws(
    () =>
      createPlystraClient({
        baseUrl: "https://user:password@example.test/?query=secret",
        credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
    credentialPolicy: anonymousCredentialPolicy,
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
