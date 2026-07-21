import {
  PlystraError,
  createCompatSendV1,
  createEmailSendV1,
  createMailDeliverV1,
  createPlystraClient,
  type ClientOptions,
  type CompatSendV1Request,
  type CompatSendV1Response,
  type EmailSendV1ErrorCode,
  type EmailSendV1Request,
  type EmailSendV1Response,
  type MailDeliverV1ErrorCode,
} from "@acme/project-sdk";

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
const classified = new PlystraError(422, "capability_error", errorCode);
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

void response;
void standaloneResponse;
void compatResponse;
void compatStandalone;
void deprecatedResponse;
void deprecatedStandalone;
void deprecatedError;
void classified;
void revision;
void positions;
void prefixOperation;
void nestedPrefixOperation;
void hyphenatedOperation;
