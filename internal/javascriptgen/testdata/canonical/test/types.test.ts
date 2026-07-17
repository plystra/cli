import {
  PlystraError,
  createEmailSendV1,
  createPlystraClient,
  type ClientOptions,
  type EmailSendV1ErrorCode,
  type EmailSendV1Request,
  type EmailSendV1Response,
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
  to: "person@example.com",
  tags: ["welcome"],
  priority: "urgent",
  retries: 2,
};
const response: Promise<EmailSendV1Response> = client.email.send.v1(request);
const errorCode: EmailSendV1ErrorCode = "invalid_recipient";
const standalone = createEmailSendV1(options);
const standaloneResponse: Promise<EmailSendV1Response> = standalone(request);
const classified = new PlystraError(422, "capability_error", errorCode);
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
// @ts-expect-error exact versions remain visible.
client.email.send.v2(request);

void response;
void standaloneResponse;
void classified;
void prefixOperation;
void nestedPrefixOperation;
void hyphenatedOperation;
