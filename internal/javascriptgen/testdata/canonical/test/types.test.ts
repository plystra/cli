import {
  PlystraError,
  createCompatSendV1,
  createEmailSendV1,
  createMailDeliverV1,
  createPlystraClient,
  createRecordsEchoV1,
  type ClientOptions,
  type CompatSendV1Request,
  type CompatSendV1Response,
  type EmailSendV1ErrorCode,
  type EmailSendV1Request,
  type EmailSendV1Response,
  type MailDeliverV1ErrorCode,
  type KernelErrorClass,
  type PlystraErrorDetail,
  type RecordsEchoV1Detail,
  type RecordsEchoV1Envelope,
  type RecordsEchoV1ErrorCode,
  type RecordsEchoV1Request,
  type RecordsEchoV1Response,
} from "@acme/project-sdk";

// @ts-expect-error raw Connect errors are not exported by the Plystra SDK.
import { ConnectError } from "@acme/project-sdk";
// @ts-expect-error raw Connect transport types are not exported by the Plystra SDK.
import type { Transport } from "@acme/project-sdk";
// @ts-expect-error internal generated modules are blocked by the package export map.
import { createRuntime } from "@acme/project-sdk/dist/runtime.js";

const options: ClientOptions = {
  baseUrl: "https://api.example.test",
  fetch: async () =>
    new Response('{"accepted":true}', {
      headers: { "Content-Type": "application/json" },
    }),
};
const client = createPlystraClient(options);
const request: EmailSendV1Request = {
  checkpoints: [
    -9_223_372_036_854_775_808n,
    9_223_372_036_854_775_807n,
  ],
  offset: -9_223_372_036_854_775_808n,
  to: "person@example.com",
  tags: ["welcome"],
  priority: "urgent",
  retries: 9_223_372_036_854_775_807n,
};
const response: Promise<EmailSendV1Response> = client.email.send.v1(request);
const errorCode: EmailSendV1ErrorCode = "invalid_recipient";
const standalone = createEmailSendV1(options);
const standaloneResponse: Promise<EmailSendV1Response> = standalone(request);
const compatRequest: CompatSendV1Request = request;
const compatResponse: Promise<CompatSendV1Response> =
  client.compat.send.v1(compatRequest);
const compatStandalone: Promise<EmailSendV1Response> =
  createCompatSendV1(options)(request);
const deprecatedResponse: Promise<EmailSendV1Response> =
  client.mail.deliver.v1(request);
const deprecatedStandalone: Promise<EmailSendV1Response> =
  createMailDeliverV1(options)(request);
const deprecatedError: MailDeliverV1ErrorCode = errorCode;
const safeDetail: PlystraErrorDetail = {
  requestedCapabilityID: "email.send/v1",
  canonicalCapabilityID: "email.send/v1",
  semanticErrorCode: errorCode,
};
const kernelClass: KernelErrorClass = "unavailable";
const classified = new PlystraError(422, "capability_error", safeDetail);
const interfaceDetail: RecordsEchoV1Detail = {
  code: "nested",
  amount: -9_223_372_036_854_775_808n,
  children: [{ code: "leaf" }],
};
const interfaceEnvelope: RecordsEchoV1Envelope = {
  active: true,
  count_32: -2_147_483_648,
  count_64: -9_223_372_036_854_775_808n,
  unsigned_32: 4_294_967_295,
  unsigned_64: 18_446_744_073_709_551_615n,
  ratio_32: 1.25,
  ratio_64: -9.5,
  name: "canonical",
  payload: new Uint8Array([0, 1, 2]),
  tags: ["one", "two"],
  scores: { first: 10n, second: -20n },
  detail: interfaceDetail,
  items: [interfaceDetail],
  lookup: { nested: interfaceDetail },
  at: "2026-07-25T01:02:03.456Z",
  delay: "1.500s",
  payloads: [new Uint8Array([3, 4])],
  identifiers: { "-1": 9n },
};
const interfaceRequest: RecordsEchoV1Request = { value: interfaceEnvelope };
const interfaceResponse: RecordsEchoV1Response = { value: interfaceEnvelope };
const interfaceErrorCode: RecordsEchoV1ErrorCode = "record_rejected";
const interfaceMethodResponse: Promise<RecordsEchoV1Response> =
  client.records.echo.v1(interfaceRequest);
const interfaceStandaloneResponse: Promise<RecordsEchoV1Response> =
  createRecordsEchoV1(options)(interfaceRequest);
declare const typedResponse: EmailSendV1Response;
const revision: bigint | undefined = typedResponse.revision;
const positions: ReadonlyArray<bigint> | undefined = typedResponse.positions;
const prefixOperation: Promise<Readonly<Record<string, never>>> =
  client.alpha.beta.v1({});
const nestedPrefixOperation: Promise<Readonly<Record<string, never>>> =
  client.alpha.beta.v1.check.v1({});
const hyphenatedOperation: Promise<Readonly<Record<string, never>>> =
  client["foo-bar"].send.v1({});

// @ts-expect-error required fields cannot be omitted.
client.email.send.v1({});
// @ts-expect-error enum values remain exact.
client.email.send.v1({ to: "person@example.com", tags: [], priority: "later" });
// @ts-expect-error canonical integer fields require bigint rather than imprecise number.
client.email.send.v1({ to: "person@example.com", tags: [], priority: "normal", offset: 1 });
// @ts-expect-error Alias request types remain the exact canonical target contract.
client.compat.send.v1({});
// @ts-expect-error exact versions remain visible.
client.email.send.v2(request);
// @ts-expect-error loose detail codes are not part of the Plystra error API.
classified.detailCode;
// @ts-expect-error canonical Interface methods require their exact request shape.
client.records.echo.v1({});
// @ts-expect-error only declared canonical Interface semantic errors are accepted.
const invalidInterfaceErrorCode: RecordsEchoV1ErrorCode = "private_failure";
const invalidInterfaceInteger: RecordsEchoV1Envelope = {
  ...interfaceEnvelope,
  // @ts-expect-error canonical int64 fields require exact bigint values.
  count_64: 1,
};
const invalidInterfaceBytes: RecordsEchoV1Envelope = {
  ...interfaceEnvelope,
  // @ts-expect-error canonical byte fields use Uint8Array rather than base64 strings.
  payload: "AAEC",
};

void response;
void standaloneResponse;
void compatResponse;
void compatStandalone;
void deprecatedResponse;
void deprecatedStandalone;
void deprecatedError;
void classified;
void interfaceRequest;
void interfaceResponse;
void interfaceErrorCode;
void interfaceMethodResponse;
void interfaceStandaloneResponse;
void invalidInterfaceErrorCode;
void invalidInterfaceInteger;
void invalidInterfaceBytes;
void kernelClass;
void revision;
void positions;
void prefixOperation;
void nestedPrefixOperation;
void hyphenatedOperation;
