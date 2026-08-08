import { z } from "zod";
import { request } from "./http";

// How a send authenticates (backend/internal/mailer). "plain" is a username and
// password; "xoauth2" is an OAuth2 bearer token, which is what Microsoft 365
// requires now that it has turned off Basic Authentication for SMTP AUTH.
export const SMTP_AUTH_MECHANISMS = ["plain", "xoauth2"] as const;

export type SmtpAuthMechanism = (typeof SMTP_AUTH_MECHANISMS)[number];

// The mechanism that authenticates with a bearer token instead of a password, and
// so the one the app-registration fields belong to.
export const SMTP_XOAUTH2: SmtpAuthMechanism = "xoauth2";

export function isSmtpAuthMechanism(value: string): value is SmtpAuthMechanism {
  return (SMTP_AUTH_MECHANISMS as readonly string[]).includes(value);
}

// The mechanisms a settings form offers: the ones this frontend has copy for,
// plus whatever the org already has stored when the backend has gained one this
// list does not know yet. Dropping an unknown stored value would leave the
// selector on a mechanism the org did not choose and rewrite its configuration
// on the next save.
export function smtpAuthMechanismOptions(stored: string): string[] {
  if (stored === "" || isSmtpAuthMechanism(stored)) {
    return [...SMTP_AUTH_MECHANISMS];
  }
  return [...SMTP_AUTH_MECHANISMS, stored];
}

// Per-organization SMTP configuration used to deliver credential offers and
// notifications by e-mail. The password and the OAuth client secret are
// write-only: they are never returned, only whether one is stored
// (`hasPassword` / `hasClientSecret`).
//
// authMechanism is a plain string rather than a zod enum on purpose: a mechanism
// the backend gains before this list learns it would otherwise fail the whole
// settings document and take the screen down, not just that one option.
export const emailSettingsSchema = z.object({
  configured: z.boolean(),
  host: z.string(),
  port: z.number(),
  username: z.string(),
  authMechanism: z.string(),
  tenantId: z.string(),
  clientId: z.string(),
  fromName: z.string(),
  fromAddress: z.string(),
  enabled: z.boolean(),
  hasPassword: z.boolean(),
  hasClientSecret: z.boolean(),
  updatedAt: z.string().optional(),
});

export type EmailSettings = z.infer<typeof emailSettingsSchema>;

export interface EmailSettingsInput {
  host: string;
  port: number;
  username: string;
  // null keeps the stored password, a non-empty string sets it, "" clears it.
  password: string | null;
  authMechanism: string;
  tenantId: string;
  clientId: string;
  // Same three-way rule as password.
  clientSecret: string | null;
  fromName: string;
  fromAddress: string;
  enabled: boolean;
}

// The settings form's raw fields, before they become a request body. The two
// secrets are plain strings here because the form only ever holds what was typed
// into them: blank means the admin left the field alone.
export interface EmailSettingsForm {
  host: string;
  port: number;
  username: string;
  password: string;
  authMechanism: string;
  tenantId: string;
  clientId: string;
  clientSecret: string;
  fromName: string;
  fromAddress: string;
  enabled: boolean;
}

// emailSettingsBody turns the form's fields into the body to save.
//
// The credentials belonging to the mechanism that is *not* selected are cleared
// rather than left at rest: the form unmounts the input for one of the two, so
// there would otherwise be no way to blank it and an org's unused client secret or
// SMTP password would stay encrypted in its row indefinitely after it stopped
// being used. Switching back means entering the secret again, which is the trade
// this makes deliberately in favour of storing no credential the org is not using.
//
// Username is cleared in neither direction: plain authenticates with it, and
// XOAUTH2 uses it to override the mailbox the token submits as.
export function emailSettingsBody(form: EmailSettingsForm): EmailSettingsInput {
  const usesOAuth = form.authMechanism === SMTP_XOAUTH2;
  return {
    host: form.host.trim(),
    port: form.port,
    username: form.username.trim(),
    password: usesOAuth ? "" : keptOrSet(form.password),
    authMechanism: form.authMechanism,
    tenantId: usesOAuth ? form.tenantId.trim() : "",
    clientId: usesOAuth ? form.clientId.trim() : "",
    clientSecret: usesOAuth ? keptOrSet(form.clientSecret) : "",
    fromName: form.fromName.trim(),
    fromAddress: form.fromAddress.trim(),
    enabled: form.enabled,
  };
}

// A field the admin left blank keeps the stored secret (null); a typed value
// replaces it. Clearing is the caller's decision, not a blank field's.
function keptOrSet(secret: string): string | null {
  return secret ? secret : null;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/email`;
}

export function getEmailSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<EmailSettings> {
  return request(`${base(slug)}/settings`, {
    schema: emailSettingsSchema,
    signal,
  });
}

export function updateEmailSettings(
  slug: string,
  input: EmailSettingsInput,
  signal?: AbortSignal,
): Promise<EmailSettings> {
  return request(`${base(slug)}/settings`, {
    schema: emailSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function sendTestEmail(
  slug: string,
  input: TestEmailInput,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/test`, {
    schema: z.void(),
    method: "POST",
    body: input,
    signal,
  });
}

// A test send without a kind is the SMTP self-test in the deployment's default
// language; with one it is a real specimen of that cause, rendered from the org's
// own template.
export interface TestEmailInput {
  to: string;
  kind?: MailTemplateKind;
  locale?: MailLocale;
}

// Transactional mail templates. Kinds and locales are a closed backend set
// (backend/internal/email/catalog.go), so the frontend never invents one: it
// renders the matrix the list endpoint returns.
export const MAIL_TEMPLATE_KINDS = [
  "credential_offer",
  "event_notification",
  "invitation",
  "postguard_file",
  "signed_document",
  "smtp_test",
] as const;

export type MailTemplateKind = (typeof MAIL_TEMPLATE_KINDS)[number];

export const MAIL_LOCALES = ["en", "nl"] as const;

export type MailLocale = (typeof MAIL_LOCALES)[number];

const mailTemplateKindSchema = z.enum(MAIL_TEMPLATE_KINDS);
const mailLocaleSchema = z.enum(MAIL_LOCALES);

// The block types a layout may be composed of. A closed backend set
// (backend/internal/email/catalog.go): every type renders through the
// mail-client-safe shell, so a tenant composes blocks, never HTML.
export const MAIL_BLOCK_TYPES = [
  "logo",
  "heading",
  "paragraph",
  "button",
  "divider",
  "footer",
] as const;

export type MailBlockType = (typeof MAIL_BLOCK_TYPES)[number];

// One layout block. Which fields apply depends on the type: text belongs to
// heading, paragraph and footer blocks; label, url and linkFallback to button
// blocks; logo and divider carry nothing. The backend rejects a field on a block
// type it does not belong to.
export const mailBlockSchema = z.object({
  type: z.enum(MAIL_BLOCK_TYPES),
  text: z.string().optional().default(""),
  label: z.string().optional().default(""),
  url: z.string().optional().default(""),
  linkFallback: z.string().optional().default(""),
});

export type MailBlock = z.infer<typeof mailBlockSchema>;

// A template is a subject plus an ordered block layout, never HTML: the
// mail-client-safe markup is the backend shell's. Every text field may reference
// the kind's variables as {{name}} placeholders.
export const mailTemplateSchema = z.object({
  subject: z.string(),
  preheader: z.string().optional().default(""),
  blocks: z.array(mailBlockSchema),
});

export type MailTemplate = z.infer<typeof mailTemplateSchema>;

export const mailTemplateVariableSchema = z.object({
  name: z.string(),
  // A URL variable is the only kind of variable that may stand in for the call to
  // action's link.
  isUrl: z.boolean(),
});

export type MailTemplateVariable = z.infer<typeof mailTemplateVariableSchema>;

const mailTemplateSummarySchema = z.object({
  locale: mailLocaleSchema,
  customized: z.boolean(),
  subject: z.string(),
  updatedAt: z.string().optional(),
});

export type MailTemplateSummary = z.infer<typeof mailTemplateSummarySchema>;

const mailTemplateKindSummarySchema = z.object({
  kind: mailTemplateKindSchema,
  variables: z.array(mailTemplateVariableSchema),
  locales: z.array(mailTemplateSummarySchema),
});

export type MailTemplateKindSummary = z.infer<
  typeof mailTemplateKindSummarySchema
>;

export const mailTemplateListSchema = z.object({
  kinds: z.array(mailTemplateKindSummarySchema),
});

export type MailTemplateList = z.infer<typeof mailTemplateListSchema>;

export const mailTemplateDetailSchema = z.object({
  kind: mailTemplateKindSchema,
  locale: mailLocaleSchema,
  customized: z.boolean(),
  updatedAt: z.string().optional(),
  template: mailTemplateSchema,
  // The shipped copy this template reverts to, so the editor can offer a revert
  // and show what the default says.
  default: mailTemplateSchema,
  variables: z.array(mailTemplateVariableSchema),
});

export type MailTemplateDetail = z.infer<typeof mailTemplateDetailSchema>;

export const mailPreviewSchema = z.object({
  subject: z.string(),
  html: z.string(),
  text: z.string(),
});

export type MailPreview = z.infer<typeof mailPreviewSchema>;

export interface MailTemplateRef {
  kind: MailTemplateKind;
  locale: MailLocale;
}

function templatePath(slug: string, ref: MailTemplateRef): string {
  return `${base(slug)}/templates/${ref.kind}/${ref.locale}`;
}

export function getMailTemplates(
  slug: string,
  signal?: AbortSignal,
): Promise<MailTemplateList> {
  return request(`${base(slug)}/templates`, {
    schema: mailTemplateListSchema,
    signal,
  });
}

export function getMailTemplate(
  slug: string,
  ref: MailTemplateRef,
  signal?: AbortSignal,
): Promise<MailTemplateDetail> {
  return request(templatePath(slug, ref), {
    schema: mailTemplateDetailSchema,
    signal,
  });
}

export function updateMailTemplate(
  slug: string,
  ref: MailTemplateRef,
  template: MailTemplate,
  signal?: AbortSignal,
): Promise<MailTemplateDetail> {
  return request(templatePath(slug, ref), {
    schema: mailTemplateDetailSchema,
    method: "PUT",
    body: template,
    signal,
  });
}

// Reverting drops the org's own copy, so the response carries the shipped default
// that is now in force again.
export function resetMailTemplate(
  slug: string,
  ref: MailTemplateRef,
  signal?: AbortSignal,
): Promise<MailTemplateDetail> {
  return request(templatePath(slug, ref), {
    schema: mailTemplateDetailSchema,
    method: "DELETE",
    signal,
  });
}

// The backend renders the preview so it cannot drift from what is delivered.
// Passing no template previews what is currently in force.
export function previewMailTemplate(
  slug: string,
  ref: MailTemplateRef,
  template: MailTemplate | null,
  signal?: AbortSignal,
): Promise<MailPreview> {
  return request(`${base(slug)}/templates/${ref.kind}/preview`, {
    schema: mailPreviewSchema,
    method: "POST",
    body: { locale: ref.locale, template },
    signal,
  });
}
