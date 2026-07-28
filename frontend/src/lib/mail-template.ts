import type { MailTemplate, MailTemplateVariable } from "../api/email";

// Client-side checks for the mail-template editor. These mirror the rules
// backend/internal/email/render.go's ValidateTemplate enforces, so a tenant sees
// the problem beside the field they are typing in rather than only after a failed
// save. The backend stays the authority: its 400 is surfaced verbatim, and these
// checks never gate anything the backend would have accepted.
//
// Keep the two in step. The rules are: every {{placeholder}} must be one of the
// kind's declared variables, a leftover "{{" is a malformed placeholder, subject
// and headline are required, the call-to-action label and URL are set together or
// not at all, and the URL is either exactly one declared URL variable or an
// absolute http(s) literal — never a mix.

/** Matches a {{name}} placeholder, tolerating inner spaces, as the backend does. */
const PLACEHOLDER_PATTERN = /\{\{\s*([A-Za-z][A-Za-z0-9]*)\s*\}\}/g;

/** The single-value prose fields, in the order the editor shows them. */
export const MAIL_TEMPLATE_TEXT_FIELDS = [
  "subject",
  "preheader",
  "headline",
  "ctaLabel",
  "ctaUrl",
  "linkFallback",
  "note",
  "footer",
] as const;

export type MailTemplateTextField = (typeof MAIL_TEMPLATE_TEXT_FIELDS)[number];

/** Every field an editor problem can point at. */
export type MailTemplateField = MailTemplateTextField | "paragraphs";

export type MailTemplateIssue =
  | "unknownPlaceholder"
  | "malformedPlaceholder"
  | "required"
  | "ctaPair"
  | "ctaUrlShape";

export interface MailTemplateProblem {
  field: MailTemplateField;
  issue: MailTemplateIssue;
  /** Set on "paragraphs" problems: which paragraph the problem is in. */
  paragraphIndex?: number;
  /** Set on "unknownPlaceholder": the name that is not a declared variable. */
  placeholder?: string;
}

/** Every placeholder name a piece of text references, in order of appearance. */
export function placeholdersIn(text: string): string[] {
  return [...text.matchAll(PLACEHOLDER_PATTERN)].map((match) => match[1]);
}

/** A "{{" that the placeholder syntax does not cover, e.g. "{{ org name }}". */
export function hasMalformedPlaceholder(text: string): boolean {
  return text.replace(PLACEHOLDER_PATTERN, "").includes("{{");
}

/** Wraps a variable name in the placeholder syntax, for the variable palette. */
export function placeholderFor(name: string): string {
  return `{{${name}}}`;
}

/**
 * Inserts a placeholder into `value` at `caret` (appending when caret is null),
 * and reports where the caret should land afterwards, so the editor keeps typing
 * position after a click on the variable palette.
 */
export function insertPlaceholder(
  value: string,
  caret: number | null,
  name: string,
): { value: string; caret: number } {
  const placeholder = placeholderFor(name);
  const at = caret ?? value.length;
  const clamped = Math.max(0, Math.min(at, value.length));
  return {
    value: value.slice(0, clamped) + placeholder + value.slice(clamped),
    caret: clamped + placeholder.length,
  };
}

function isAbsoluteHttpUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.host !== ""
    );
  } catch {
    return false;
  }
}

function checkPlaceholders(
  text: string,
  field: MailTemplateField,
  allowed: Set<string>,
  paragraphIndex?: number,
): MailTemplateProblem[] {
  const problems: MailTemplateProblem[] = [];
  for (const name of placeholdersIn(text)) {
    if (!allowed.has(name)) {
      problems.push({
        field,
        issue: "unknownPlaceholder",
        placeholder: name,
        paragraphIndex,
      });
    }
  }
  if (hasMalformedPlaceholder(text)) {
    problems.push({ field, issue: "malformedPlaceholder", paragraphIndex });
  }
  return problems;
}

/**
 * Every problem in a draft, in field order. An empty array means the backend
 * should accept the save.
 */
export function validateMailTemplate(
  template: MailTemplate,
  variables: readonly MailTemplateVariable[],
): MailTemplateProblem[] {
  const allowed = new Set(variables.map((variable) => variable.name));
  const problems: MailTemplateProblem[] = [];

  for (const field of MAIL_TEMPLATE_TEXT_FIELDS) {
    problems.push(...checkPlaceholders(template[field], field, allowed));
  }
  template.paragraphs.forEach((paragraph, index) => {
    problems.push(
      ...checkPlaceholders(paragraph, "paragraphs", allowed, index),
    );
  });

  if (template.subject.trim() === "") {
    problems.push({ field: "subject", issue: "required" });
  }
  if (template.headline.trim() === "") {
    problems.push({ field: "headline", issue: "required" });
  }
  if ((template.ctaLabel === "") !== (template.ctaUrl === "")) {
    problems.push({ field: "ctaUrl", issue: "ctaPair" });
  }
  if (!ctaUrlShapeIsValid(template.ctaUrl, variables)) {
    problems.push({ field: "ctaUrl", issue: "ctaUrlShape" });
  }
  return problems;
}

/**
 * The call to action is the only field that reaches an href, so it is a closed
 * shape: empty, exactly one declared URL variable, or an absolute http(s) literal
 * with no placeholders.
 */
export function ctaUrlShapeIsValid(
  value: string,
  variables: readonly MailTemplateVariable[],
): boolean {
  if (value === "") {
    return true;
  }
  const names = placeholdersIn(value);
  if (names.length > 0) {
    // A literal with a placeholder spliced into it cannot be checked without
    // rendering, so only a bare variable reference is allowed.
    if (placeholderFor(names[0]) !== value.trim() || names.length > 1) {
      return false;
    }
    return variables.some(
      (variable) => variable.name === names[0] && variable.isUrl,
    );
  }
  return isAbsoluteHttpUrl(value);
}

/** True when the draft differs from the template it was seeded from. */
export function mailTemplatesDiffer(a: MailTemplate, b: MailTemplate): boolean {
  if (MAIL_TEMPLATE_TEXT_FIELDS.some((field) => a[field] !== b[field])) {
    return true;
  }
  if (a.paragraphs.length !== b.paragraphs.length) {
    return true;
  }
  return a.paragraphs.some(
    (paragraph, index) => paragraph !== b.paragraphs[index],
  );
}
