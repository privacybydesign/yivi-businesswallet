import type {
  MailBlock,
  MailBlockType,
  MailTemplate,
  MailTemplateVariable,
} from "../api/email";

// Client-side checks for the mail-template designer. These mirror the rules
// backend/internal/email/render.go's ValidateTemplate enforces, so a tenant sees
// the problem beside the field they are typing in rather than only after a failed
// save. The backend stays the authority: its 400 is surfaced verbatim, and these
// checks never gate anything the backend would have accepted.
//
// Keep the two in step. The rules are: every {{placeholder}} must be one of the
// kind's declared variables, a leftover "{{" is a malformed placeholder, the
// subject is required, the layout needs at least one block and at least one
// heading or paragraph, a heading/paragraph/footer block needs text, a button
// block needs both a label and a URL, and the URL is either exactly one declared
// URL variable or an absolute http(s) literal — never a mix.

/** Matches a {{name}} placeholder, tolerating inner spaces, as the backend does. */
const PLACEHOLDER_PATTERN = /\{\{\s*([A-Za-z][A-Za-z0-9]*)\s*\}\}/g;

/** The backend's cap on a layout's block count (maxBlocks, render.go). */
export const MAX_MAIL_BLOCKS = 24;

/** The template-level prose fields (block fields are addressed per block). */
export const MAIL_TEMPLATE_TEXT_FIELDS = ["subject", "preheader"] as const;

export type MailTemplateTextField = (typeof MAIL_TEMPLATE_TEXT_FIELDS)[number];

/** Every place an editor problem can point at. */
export type MailTemplateField = MailTemplateTextField | "blocks";

/** The editable fields of one block. */
export type MailBlockField = "text" | "label" | "url" | "linkFallback";

export type MailTemplateIssue =
  | "unknownPlaceholder"
  | "malformedPlaceholder"
  | "required"
  | "buttonUrlShape"
  | "noBlocks"
  | "needsContent"
  | "tooManyBlocks";

export interface MailTemplateProblem {
  field: MailTemplateField;
  issue: MailTemplateIssue;
  /** Set on "blocks" problems that concern one block: its index in the layout. */
  blockIndex?: number;
  /** Set with blockIndex: which of the block's fields the problem is in. */
  blockField?: MailBlockField;
  /** Set on "unknownPlaceholder": the name that is not a declared variable. */
  placeholder?: string;
}

/** The fields the designer shows per block type, in display order. */
export const MAIL_BLOCK_FIELDS: Record<
  MailBlockType,
  readonly MailBlockField[]
> = {
  logo: [],
  heading: ["text"],
  paragraph: ["text"],
  button: ["label", "url", "linkFallback"],
  divider: [],
  footer: ["text"],
};

/** A fresh block of one type, with every field the editor binds present. */
export function newMailBlock(type: MailBlockType): MailBlock {
  return { type, text: "", label: "", url: "", linkFallback: "" };
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
  block?: { blockIndex: number; blockField: MailBlockField },
): MailTemplateProblem[] {
  const problems: MailTemplateProblem[] = [];
  for (const name of placeholdersIn(text)) {
    if (!allowed.has(name)) {
      problems.push({
        field,
        issue: "unknownPlaceholder",
        placeholder: name,
        ...block,
      });
    }
  }
  if (hasMalformedPlaceholder(text)) {
    problems.push({ field, issue: "malformedPlaceholder", ...block });
  }
  return problems;
}

/**
 * Every problem in a draft, template fields first and then per block, in layout
 * order. An empty array means the backend should accept the save.
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
  if (template.subject.trim() === "") {
    problems.push({ field: "subject", issue: "required" });
  }

  if (template.blocks.length === 0) {
    problems.push({ field: "blocks", issue: "noBlocks" });
  }
  if (template.blocks.length > MAX_MAIL_BLOCKS) {
    problems.push({ field: "blocks", issue: "tooManyBlocks" });
  }
  if (
    template.blocks.length > 0 &&
    !template.blocks.some(
      (block) => block.type === "heading" || block.type === "paragraph",
    )
  ) {
    problems.push({ field: "blocks", issue: "needsContent" });
  }

  template.blocks.forEach((block, blockIndex) => {
    for (const blockField of MAIL_BLOCK_FIELDS[block.type]) {
      problems.push(
        ...checkPlaceholders(block[blockField], "blocks", allowed, {
          blockIndex,
          blockField,
        }),
      );
    }
    switch (block.type) {
      case "heading":
      case "paragraph":
      case "footer":
        if (block.text.trim() === "") {
          problems.push({
            field: "blocks",
            issue: "required",
            blockIndex,
            blockField: "text",
          });
        }
        break;
      case "button":
        if (block.label.trim() === "") {
          problems.push({
            field: "blocks",
            issue: "required",
            blockIndex,
            blockField: "label",
          });
        }
        if (block.url.trim() === "") {
          problems.push({
            field: "blocks",
            issue: "required",
            blockIndex,
            blockField: "url",
          });
        } else if (!buttonUrlShapeIsValid(block.url, variables)) {
          problems.push({
            field: "blocks",
            issue: "buttonUrlShape",
            blockIndex,
            blockField: "url",
          });
        }
        break;
      case "logo":
      case "divider":
        break;
    }
  });
  return problems;
}

/**
 * The button URL is the only field that reaches an href, so it is a closed
 * shape: exactly one declared URL variable, or an absolute http(s) literal with
 * no placeholders.
 */
export function buttonUrlShapeIsValid(
  value: string,
  variables: readonly MailTemplateVariable[],
): boolean {
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

function blocksEqual(a: MailBlock, b: MailBlock): boolean {
  return (
    a.type === b.type &&
    a.text === b.text &&
    a.label === b.label &&
    a.url === b.url &&
    a.linkFallback === b.linkFallback
  );
}

/** True when the draft differs from the template it was seeded from. */
export function mailTemplatesDiffer(a: MailTemplate, b: MailTemplate): boolean {
  if (MAIL_TEMPLATE_TEXT_FIELDS.some((field) => a[field] !== b[field])) {
    return true;
  }
  if (a.blocks.length !== b.blocks.length) {
    return true;
  }
  return a.blocks.some((block, index) => !blocksEqual(block, b.blocks[index]));
}
