import assert from "node:assert/strict";
import test from "node:test";

import {
  PlystraError,
  createEmailSendV1,
  createPlystraClient,
} from "@acme/project-sdk";

function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("nested client serializes strict JSON and attaches one access token", async () => {
  const calls = [];
  const controller = new AbortController();
  const client = createPlystraClient({
    baseUrl: "https://api.example.test/root",
    getAccessToken: async () => "browser-token",
    fetch: async (input, init) => {
      calls.push({ input, init });
      return jsonResponse({ accepted: true, latency: 1.5 });
    },
  });

  const response = await client.email.send.v1(
    {
      to: "person@example.com",
      tags: ["welcome"],
      priority: "urgent",
      retries: 2,
      metadata: { nested: [true, null, { count: 1 }] },
    },
    { signal: controller.signal },
  );

  assert.deepEqual(response, { accepted: true, latency: 1.5 });
  assert.equal(calls.length, 1);
  const [{ input, init }] = calls;
  assert.equal(
    input.href,
    "https://api.example.test/root/api/v1/capabilities/email.send/v1/invoke",
  );
  assert.equal(init.method, "POST");
  assert.equal(init.cache, "no-store");
  assert.equal(init.credentials, "same-origin");
  assert.equal(init.redirect, "error");
  assert.equal(init.signal, controller.signal);
  const headers = new Headers(init.headers);
  assert.equal(headers.get("Accept"), "application/json");
  assert.equal(headers.get("Content-Type"), "application/json");
  assert.equal(headers.get("Authorization"), "Bearer browser-token");
  assert.deepEqual(JSON.parse(init.body), {
    metadata: { nested: [true, null, { count: 1 }] },
    priority: "urgent",
    retries: 2,
    tags: ["welcome"],
    to: "person@example.com",
  });
  assert.equal(Object.isFrozen(client), true);
  assert.equal(Object.isFrozen(client.email), true);
  assert.equal(Object.isFrozen(client.email.send), true);
});

test("tree-shakable operation factory uses the same canonical transport", async () => {
  let requested = "";
  const send = createEmailSendV1({
    baseUrl: new URL("https://api.example.test/"),
    fetch: async (input) => {
      requested = input.href;
      return jsonResponse({ accepted: true });
    },
  });
  assert.deepEqual(
    await send({ to: "person@example.com", tags: [], priority: "normal" }),
    { accepted: true },
  );
  assert.equal(
    requested,
    "https://api.example.test/api/v1/capabilities/email.send/v1/invoke",
  );
});

test("request validation rejects malformed, unsafe, and unknown values before fetch", async () => {
  let calls = 0;
  const send = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      calls++;
      return jsonResponse({ accepted: true });
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

test("response validation and error mapping expose only stable fields", async () => {
  const responses = [
    jsonResponse({ accepted: "yes" }),
    jsonResponse({ accepted: true, unexpected: true }),
    new Response("{}", {
      status: 200,
      headers: { "Content-Type": "text/plain" },
    }),
    new Response("{", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  ];
  const send = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => responses.shift(),
  });
  for (let index = 0; index < 4; index++) {
    await assert.rejects(
      () => send({ to: "person@example.com", tags: [], priority: "normal" }),
      (error) =>
        error instanceof PlystraError &&
        error.code === "invalid_response" &&
        error.message === "invalid_response",
    );
  }

  const semantic = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () =>
      jsonResponse(
        {
          error: {
            code: "capability_error",
            detail_code: "invalid_recipient",
            message: "provider secret must not escape",
          },
        },
        422,
      ),
  });
  await assert.rejects(
    () => semantic({ to: "person@example.com", tags: [], priority: "normal" }),
    (error) => {
      assert.equal(error instanceof PlystraError, true);
      assert.equal(error.status, 422);
      assert.equal(error.code, "capability_error");
      assert.equal(error.detailCode, "invalid_recipient");
      assert.equal(error.message, "capability_error");
      assert.equal("body" in error, false);
      return true;
    },
  );

  const unsafeServerCode = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () =>
      jsonResponse({ error: { code: "provider_secret" } }, 500),
  });
  await assert.rejects(
    () =>
      unsafeServerCode({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof PlystraError && error.code === "invalid_response",
  );

  const malformedFetchResult = createEmailSendV1({
    baseUrl: "https://api.example.test",
    fetch: async () => ({
      get status() {
        throw new Error("response getter secret");
      },
    }),
  });
  await assert.rejects(
    () =>
      malformedFetchResult({
        to: "person@example.com",
        tags: [],
        priority: "normal",
      }),
    (error) =>
      error instanceof PlystraError &&
      error.code === "invalid_response" &&
      error.message === "invalid_response",
  );
});

test("credentials, cancellation, and network failures are bounded and normalized", async () => {
  let calls = 0;
  const invalidCredential = createEmailSendV1({
    baseUrl: "https://api.example.test",
    getAccessToken: () => " bad-token",
    fetch: async () => {
      calls++;
      return jsonResponse({ accepted: true });
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

  const credentialFailure = createEmailSendV1({
    baseUrl: "https://api.example.test",
    getAccessToken: () => {
      throw new Error("credential secret");
    },
    fetch: async () => {
      calls++;
      return jsonResponse({ accepted: true });
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
      return jsonResponse({ accepted: true });
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
  let calls = 0;
  const client = createPlystraClient({
    baseUrl: "https://api.example.test",
    fetch: async () => {
      calls++;
      return jsonResponse({});
    },
  });
  assert.deepEqual(await client.account.profile.get.v2({}), {});
  await assert.rejects(
    () => client.account.profile.get.v2({ unexpected: true }),
    TypeError,
  );
  assert.equal(calls, 1);
});

test("prefix and hyphenated Capability paths remain callable without collisions", async () => {
  const paths = [];
  const client = createPlystraClient({
    baseUrl: "https://api.example.test",
    fetch: async (input) => {
      paths.push(input.pathname);
      return jsonResponse({});
    },
  });
  await client.alpha.beta.v1({});
  await client.alpha.beta.v1.check.v1({});
  await client["foo-bar"].send.v1({});
  await client.foo["bar-send"].v1({});
  assert.deepEqual(paths, [
    "/api/v1/capabilities/alpha.beta/v1/invoke",
    "/api/v1/capabilities/alpha.beta.v1.check/v1/invoke",
    "/api/v1/capabilities/foo-bar.send/v1/invoke",
    "/api/v1/capabilities/foo.bar-send/v1/invoke",
  ]);
});
