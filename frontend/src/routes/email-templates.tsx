import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import {
  MAIL_BLOCK_TYPES,
  MAIL_LOCALES,
  type MailBlock,
  type MailBlockType,
  type MailLocale,
  type MailTemplate,
  type MailTemplateDetail,
  type MailTemplateKind,
  type MailTemplateKindSummary,
  type MailTemplateRef,
  type MailTemplateVariable,
} from "../api/email";
import {
  useMailTemplateQuery,
  useMailTemplatesQuery,
  usePreviewMailTemplateMutation,
  useResetMailTemplateMutation,
  useSendTestEmailMutation,
  useUpdateMailTemplateMutation,
} from "../api/email.queries";
import { ApiError } from "../api/http";
import {
  MAIL_BLOCK_FIELDS,
  MAX_MAIL_BLOCKS,
  insertPlaceholder,
  mailTemplatesDiffer,
  newMailBlock,
  placeholderFor,
  validateMailTemplate,
  type MailBlockField,
  type MailTemplateProblem,
  type MailTemplateTextField,
} from "../lib/mail-template";
import { Button, Card, ConfirmDialog, Input, Table, Tag } from "../ui";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";
const CONFLICT_STATUS = 409;
// The preview is a whole rendered message; this keeps the frame tall enough to
// read one without scrolling the page.
const PREVIEW_HEIGHT = "h-[420px]";
// Plausible address check only; the backend is the authority.
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// The template-level fields above the block layout.
const TEMPLATE_FIELDS = ["subject", "preheader"] as const;

// The block fields that take a whole sentence or more get a textarea; the rest
// an input.
const MULTILINE_BLOCK_FIELDS: readonly MailBlockField[] = ["text"];
const SINGLE_LINE_BLOCK_TYPES: readonly MailBlockType[] = ["heading"];

function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

// The backend's own reason for refusing a template names the field, so it is
// shown as-is rather than replaced by a generic message.
function backendReason(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "error" in error.body &&
    errorCode(error) === "invalid_template"
  ) {
    const { error: message } = error.body;
    return typeof message === "string" ? message : null;
  }
  return null;
}

function problemMessage(problem: MailTemplateProblem, t: TFunction): string {
  switch (problem.issue) {
    case "unknownPlaceholder":
      return t("mailTemplates.problems.unknownPlaceholder", {
        placeholder: problem.placeholder ?? "",
      });
    case "malformedPlaceholder":
      return t("mailTemplates.problems.malformedPlaceholder");
    case "required":
      return t("mailTemplates.problems.required");
    case "buttonUrlShape":
      return t("mailTemplates.problems.buttonUrlShape");
    case "noBlocks":
      return t("mailTemplates.problems.noBlocks");
    case "needsContent":
      return t("mailTemplates.problems.needsContent");
    case "tooManyBlocks":
      return t("mailTemplates.problems.tooManyBlocks", {
        max: MAX_MAIL_BLOCKS,
      });
  }
}

// FieldProblems renders every problem attached to one place: a template field,
// one block's field, or the layout as a whole.
function FieldProblems({
  problems,
  field,
  blockIndex,
  blockField,
}: {
  problems: readonly MailTemplateProblem[];
  field: MailTemplateTextField | "blocks";
  blockIndex?: number;
  blockField?: MailBlockField;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const mine = problems.filter(
    (problem) =>
      problem.field === field &&
      problem.blockIndex === blockIndex &&
      problem.blockField === blockField,
  );
  if (mine.length === 0) {
    return null;
  }
  return (
    <>
      {mine.map((problem) => (
        <p
          key={`${problem.issue}${problem.placeholder ?? ""}`}
          role="alert"
          className="text-error text-[12.5px]"
        >
          {problemMessage(problem, t)}
        </p>
      ))}
    </>
  );
}

// VariablePalette offers exactly the placeholders this kind declares. Clicking one
// inserts it at the caret of whichever field was last focused, so the tenant does
// not have to type the brace syntax.
function VariablePalette({
  variables,
  onInsert,
}: {
  variables: readonly MailTemplateVariable[];
  onInsert: (name: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="border-line bg-surface-2 rounded-yivi border p-3">
      <span className={EYEBROW}>{t("mailTemplates.variables")}</span>
      <p className="text-ink-soft mt-1 text-[12.5px]">
        {t("mailTemplates.variablesHint")}
      </p>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {variables.map((variable) => (
          <button
            key={variable.name}
            type="button"
            onClick={() => onInsert(variable.name)}
            className="border-line-strong bg-surface text-ink hover:bg-surface-3 rounded-yivi cursor-pointer border px-2 py-1 font-mono text-[12px] transition-colors duration-150"
          >
            {placeholderFor(variable.name)}
          </button>
        ))}
      </div>
    </div>
  );
}

// BlockCard is one block of the layout: its type, its editable fields, and the
// reorder/remove controls. Reordering is a pair of buttons rather than drag and
// drop so it works with a keyboard and a screen reader.
function BlockCard({
  block,
  index,
  count,
  problems,
  onChange,
  onMove,
  onRemove,
  onFocusField,
}: {
  block: MailBlock;
  index: number;
  count: number;
  problems: readonly MailTemplateProblem[];
  onChange: (field: MailBlockField, value: string) => void;
  onMove: (direction: -1 | 1) => void;
  onRemove: () => void;
  onFocusField: (
    element: HTMLInputElement | HTMLTextAreaElement | null,
  ) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const number = index + 1;
  const fields = MAIL_BLOCK_FIELDS[block.type];

  return (
    <div className="border-line rounded-yivi border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className={EYEBROW}>
          {t(`mailTemplates.blocks.${block.type}`)}
        </span>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            icon="chevron_up"
            iconOnly
            aria-label={t("mailTemplates.moveBlockUp", { number })}
            disabled={index === 0}
            onClick={() => onMove(-1)}
          />
          <Button
            variant="ghost"
            size="sm"
            icon="chevron_down"
            iconOnly
            aria-label={t("mailTemplates.moveBlockDown", { number })}
            disabled={index === count - 1}
            onClick={() => onMove(1)}
          />
          <Button
            variant="dangerGhost"
            size="sm"
            icon="delete"
            iconOnly
            aria-label={t("mailTemplates.removeBlock", { number })}
            onClick={onRemove}
          />
        </div>
      </div>

      {fields.length === 0 ? (
        <p className="text-ink-soft mt-1 text-[12.5px]">
          {block.type === "logo"
            ? t("mailTemplates.blockHints.logo")
            : t("mailTemplates.blockHints.divider")}
        </p>
      ) : (
        <div className="mt-2 flex flex-col gap-2">
          {fields.map((field) => {
            const id = `mail-block-${index}-${field}`;
            const multiline =
              MULTILINE_BLOCK_FIELDS.includes(field) &&
              !SINGLE_LINE_BLOCK_TYPES.includes(block.type);
            const shared = {
              id,
              name: field,
              value: block[field],
              "data-block-index": index,
              "data-block-field": field,
              onFocus: (
                event: React.FocusEvent<HTMLInputElement | HTMLTextAreaElement>,
              ) => {
                onFocusField(event.currentTarget);
              },
            };
            return (
              <div key={field} className="flex flex-col gap-1">
                <label htmlFor={id} className={EYEBROW}>
                  {t(`mailTemplates.blockFields.${field}`)}
                </label>
                {multiline ? (
                  <textarea
                    {...shared}
                    className={`${CONTROL} min-h-20 py-2 leading-relaxed`}
                    onChange={(event) => onChange(field, event.target.value)}
                  />
                ) : (
                  <Input
                    {...shared}
                    onChange={(event) => onChange(field, event.target.value)}
                    autoComplete="off"
                  />
                )}
                <FieldProblems
                  problems={problems}
                  field="blocks"
                  blockIndex={index}
                  blockField={field}
                />
              </div>
            );
          })}
          {block.type === "button" && (
            <p className="text-ink-soft text-[12.5px]">
              {t("mailTemplates.buttonHint")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

// A block in the editor carries a stable client-side key, so React reconciles a
// reordered list by identity rather than by position — with a bare index key,
// moving a block would leave focus and per-node DOM state on the wrong block.
interface DraftBlock extends MailBlock {
  key: number;
}

interface Draft {
  subject: string;
  preheader: string;
  blocks: DraftBlock[];
}

function seedDraft(template: MailTemplate): Draft {
  return {
    subject: template.subject,
    preheader: template.preheader,
    blocks: template.blocks.map((block, key) => ({ ...block, key })),
  };
}

function toTemplate(draft: Draft): MailTemplate {
  return {
    subject: draft.subject,
    preheader: draft.preheader,
    blocks: draft.blocks.map((block) => ({
      type: block.type,
      text: block.text,
      label: block.label,
      url: block.url,
      linkFallback: block.linkFallback,
    })),
  };
}

// TemplateEditor holds the draft for one kind × locale. It is remounted (via a key
// on the loaded template) whenever the stored template changes, so the draft is
// always seeded from what is in force.
function TemplateEditor({
  slug,
  detail,
  onClose,
}: {
  slug: string;
  detail: MailTemplateDetail;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const ref: MailTemplateRef = { kind: detail.kind, locale: detail.locale };
  const save = useUpdateMailTemplateMutation(slug, ref);
  const reset = useResetMailTemplateMutation(slug, ref);
  const preview = usePreviewMailTemplateMutation(slug, ref);
  const test = useSendTestEmailMutation(slug);
  const me = useMeQuery();

  const [draft, setDraft] = useState<Draft>(() => seedDraft(detail.template));
  // Keys handed to newly added blocks; the seed used 0..n-1.
  const nextKey = useRef(detail.template.blocks.length);
  const [confirmReset, setConfirmReset] = useState(false);
  const [showText, setShowText] = useState(false);
  // null means "not typed in yet", so the field seeds from the signed-in address
  // without making it impossible to clear.
  const [testTo, setTestTo] = useState<string | null>(null);
  // The field the tenant last typed in, so the variable palette knows where to
  // insert. Kept in a ref: it must not re-render the form on every focus change.
  const focused = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);

  const template = toTemplate(draft);
  const problems = validateMailTemplate(template, detail.variables);
  const dirty = mailTemplatesDiffer(template, detail.template);
  const recipient = (testTo ?? me.data?.email ?? "").trim();

  function setField(field: (typeof TEMPLATE_FIELDS)[number], value: string) {
    setDraft((prev) => ({ ...prev, [field]: value }));
  }

  function setBlockField(index: number, field: MailBlockField, value: string) {
    setDraft((prev) => ({
      ...prev,
      blocks: prev.blocks.map((block, i) =>
        i === index ? { ...block, [field]: value } : block,
      ),
    }));
  }

  function addBlock(type: MailBlockType): void {
    setDraft((prev) => ({
      ...prev,
      blocks: [
        ...prev.blocks,
        { ...newMailBlock(type), key: nextKey.current++ },
      ],
    }));
  }

  function moveBlock(index: number, direction: -1 | 1): void {
    setDraft((prev) => {
      const target = index + direction;
      if (target < 0 || target >= prev.blocks.length) {
        return prev;
      }
      const blocks = [...prev.blocks];
      [blocks[index], blocks[target]] = [blocks[target], blocks[index]];
      return { ...prev, blocks };
    });
  }

  function removeBlock(index: number): void {
    setDraft((prev) => ({
      ...prev,
      blocks: prev.blocks.filter((_, i) => i !== index),
    }));
  }

  // Inserting into the last focused field keeps the caret where the tenant left
  // it, which is why this reads the DOM node rather than re-deriving from state.
  function insert(name: string): void {
    const element = focused.current;
    if (!element) {
      return;
    }
    const { value, caret } = insertPlaceholder(
      element.value,
      element.selectionStart,
      name,
    );
    const index = element.dataset.blockIndex;
    if (index !== undefined) {
      setBlockField(
        Number(index),
        element.dataset.blockField as MailBlockField,
        value,
      );
    } else {
      setField(element.name as (typeof TEMPLATE_FIELDS)[number], value);
    }
    // Restoring focus and caret after React has re-rendered the controlled value.
    requestAnimationFrame(() => {
      element.focus();
      element.setSelectionRange(caret, caret);
    });
  }

  function handleSave(): void {
    if (problems.length > 0 || save.isPending) {
      return;
    }
    save.mutate(template, {
      onSuccess: (saved) => {
        nextKey.current = saved.template.blocks.length;
        setDraft(seedDraft(saved.template));
      },
    });
  }

  function handlePreview(): void {
    preview.mutate(problems.length > 0 ? null : template);
  }

  function handleTest(): void {
    if (!EMAIL_PATTERN.test(recipient) || test.isPending) {
      return;
    }
    test.mutate({ to: recipient, kind: detail.kind, locale: detail.locale });
  }

  return (
    <Card className="p-7">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold">
            {t(`mailTemplates.kinds.${detail.kind}`)}
            {" · "}
            {t(`mailTemplates.locales.${detail.locale}`)}
          </h3>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t(`mailTemplates.kindDescriptions.${detail.kind}`)}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {detail.customized ? (
            <Tag tone="blue">{t("mailTemplates.customized")}</Tag>
          ) : (
            <Tag>{t("mailTemplates.default")}</Tag>
          )}
          <Button variant="ghost" size="sm" onClick={onClose}>
            {t("common.close")}
          </Button>
        </div>
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-3">
          <VariablePalette variables={detail.variables} onInsert={insert} />

          {TEMPLATE_FIELDS.map((field) => (
            <div key={field} className="flex flex-col gap-1">
              <label htmlFor={`mail-template-${field}`} className={EYEBROW}>
                {t(`mailTemplates.fields.${field}`)}
              </label>
              <Input
                name={field}
                id={`mail-template-${field}`}
                value={draft[field]}
                onFocus={(event) => {
                  focused.current = event.currentTarget;
                }}
                onChange={(event) => setField(field, event.target.value)}
                autoComplete="off"
              />
              <FieldProblems problems={problems} field={field} />
            </div>
          ))}

          <div className="flex flex-col gap-2">
            <span className={EYEBROW}>{t("mailTemplates.layout")}</span>
            <FieldProblems problems={problems} field="blocks" />
            {draft.blocks.map((block, index) => (
              <BlockCard
                key={block.key}
                block={block}
                index={index}
                count={draft.blocks.length}
                problems={problems}
                onChange={(field, value) => setBlockField(index, field, value)}
                onMove={(direction) => moveBlock(index, direction)}
                onRemove={() => removeBlock(index)}
                onFocusField={(element) => {
                  focused.current = element;
                }}
              />
            ))}
          </div>

          <div className="flex flex-col gap-1">
            <span className={EYEBROW}>{t("mailTemplates.addBlock")}</span>
            <div className="flex flex-wrap gap-1.5">
              {MAIL_BLOCK_TYPES.map((type) => (
                <Button
                  key={type}
                  variant="secondary"
                  size="sm"
                  icon="add"
                  disabled={draft.blocks.length >= MAX_MAIL_BLOCKS}
                  onClick={() => addBlock(type)}
                >
                  {t(`mailTemplates.blocks.${type}`)}
                </Button>
              ))}
            </div>
            {draft.blocks.length >= MAX_MAIL_BLOCKS && (
              <p className="text-ink-soft text-[12.5px]">
                {t("mailTemplates.problems.tooManyBlocks", {
                  max: MAX_MAIL_BLOCKS,
                })}
              </p>
            )}
          </div>

          {save.isError && (
            <p role="alert" className="text-error text-[13px]">
              {backendReason(save.error) ??
                t("mailTemplates.saveError", { message: save.error.message })}
            </p>
          )}
          {reset.isError && (
            <p role="alert" className="text-error text-[13px]">
              {t("mailTemplates.resetError", { message: reset.error.message })}
            </p>
          )}

          <div className="flex flex-wrap items-center gap-2">
            <Button
              onClick={handleSave}
              loading={save.isPending}
              disabled={problems.length > 0 || !dirty}
            >
              {t("common.save")}
            </Button>
            {detail.customized && (
              <Button
                variant="secondary"
                onClick={() => setConfirmReset(true)}
                loading={reset.isPending}
              >
                {t("mailTemplates.revert")}
              </Button>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className={EYEBROW}>{t("mailTemplates.preview")}</span>
            <div className="flex items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                icon="view"
                onClick={handlePreview}
                loading={preview.isPending}
              >
                {t("mailTemplates.refreshPreview")}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setShowText((prev) => !prev)}
              >
                {showText
                  ? t("mailTemplates.showHtml")
                  : t("mailTemplates.showText")}
              </Button>
            </div>
          </div>

          {problems.length > 0 && (
            <p className="text-ink-soft text-[12.5px]">
              {t("mailTemplates.previewBlocked")}
            </p>
          )}
          {preview.isError && (
            <p role="alert" className="text-error text-[13px]">
              {backendReason(preview.error) ??
                t("mailTemplates.previewError", {
                  message: preview.error.message,
                })}
            </p>
          )}

          {preview.data ? (
            <>
              <p className="text-ink text-[13px]">
                <span className="text-ink-soft">
                  {t("mailTemplates.previewSubject")}
                </span>{" "}
                {preview.data.subject}
              </p>
              {showText ? (
                <pre className="border-line bg-surface-2 rounded-yivi text-ink overflow-auto border p-3 text-[12.5px] whitespace-pre-wrap">
                  {preview.data.text}
                </pre>
              ) : (
                // The message is rendered by the backend, the same HTML a
                // recipient gets. It is shown in a fully sandboxed frame so the
                // preview cannot run script or navigate the app, and with no
                // background of its own: the mail document paints its own (light)
                // page colour, and an app token here would tint it in dark mode.
                <iframe
                  title={t("mailTemplates.previewFrameTitle")}
                  sandbox=""
                  srcDoc={preview.data.html}
                  className={`border-line rounded-yivi w-full border ${PREVIEW_HEIGHT}`}
                />
              )}
            </>
          ) : (
            <p className="text-ink-soft text-[13px]">
              {t("mailTemplates.previewEmpty")}
            </p>
          )}

          <div className="border-line rounded-yivi border p-3">
            <span className={EYEBROW}>{t("mailTemplates.sendTest")}</span>
            <p className="text-ink-soft mt-1 text-[12.5px]">
              {t("mailTemplates.sendTestHint")}
            </p>
            <div className="mt-2 flex flex-wrap gap-2">
              <div className="min-w-[200px] flex-1">
                <Input
                  type="email"
                  value={testTo ?? me.data?.email ?? ""}
                  onChange={(event) => setTestTo(event.target.value)}
                  aria-label={t("mailTemplates.testRecipient")}
                  autoComplete="off"
                />
              </div>
              <Button
                icon="email"
                variant="secondary"
                onClick={handleTest}
                loading={test.isPending}
                disabled={!EMAIL_PATTERN.test(recipient)}
              >
                {t("mailTemplates.send")}
              </Button>
            </div>
            {test.isError && (
              <p role="alert" className="text-error mt-2 text-[13px]">
                {errorCode(test.error) === "not_configured" &&
                test.error instanceof ApiError &&
                test.error.status === CONFLICT_STATUS
                  ? t("mailTemplates.testNotConfigured")
                  : t("mailTemplates.testError", {
                      message: test.error.message,
                    })}
              </p>
            )}
          </div>
        </div>
      </div>

      {confirmReset && (
        <ConfirmDialog
          title={t("mailTemplates.revertTitle")}
          message={t("mailTemplates.revertConfirm")}
          confirmLabel={t("mailTemplates.revert")}
          busy={reset.isPending}
          onClose={() => setConfirmReset(false)}
          onConfirm={() => {
            reset.mutate(undefined, {
              onSuccess: (reverted) => {
                nextKey.current = reverted.template.blocks.length;
                setDraft(seedDraft(reverted.template));
                setConfirmReset(false);
              },
            });
          }}
        />
      )}
    </Card>
  );
}

function TemplateRow({
  summary,
  locale,
  selected,
  onSelect,
}: {
  summary: MailTemplateKindSummary;
  locale: MailLocale;
  selected: boolean;
  onSelect: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const cell = summary.locales.find((entry) => entry.locale === locale);
  return (
    <Table.Row className={selected ? "bg-surface-2" : undefined}>
      <Table.Cell>
        <span className="font-medium">
          {t(`mailTemplates.kinds.${summary.kind}`)}
        </span>
      </Table.Cell>
      <Table.Cell className="text-ink-soft">{cell?.subject ?? ""}</Table.Cell>
      <Table.Cell>
        {cell?.customized ? (
          <Tag tone="blue">{t("mailTemplates.customized")}</Tag>
        ) : (
          <Tag>{t("mailTemplates.default")}</Tag>
        )}
      </Table.Cell>
      <Table.Cell className="text-right">
        <Button variant="secondary" size="sm" icon="edit" onClick={onSelect}>
          {t("mailTemplates.edit")}
        </Button>
      </Table.Cell>
    </Table.Row>
  );
}

// EmailTemplatesPanel is the mail-template designer, rendered as a tab on the
// Settings page. Kinds and locales come from the backend catalogue, so the panel
// renders the matrix it is given rather than a list of its own.
export function EmailTemplatesPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const templates = useMailTemplatesQuery(slug);
  const [locale, setLocale] = useState<MailLocale>(MAIL_LOCALES[0]);
  const [selected, setSelected] = useState<MailTemplateKind | null>(null);
  const detail = useMailTemplateQuery(
    slug,
    selected ? { kind: selected, locale } : null,
  );

  if (templates.isError) {
    return (
      <Card className="p-6">
        <p role="alert" className="text-error text-[14px]">
          {t("mailTemplates.loadError", { message: templates.error.message })}
        </p>
      </Card>
    );
  }
  if (templates.isPending) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <Card className="p-7">
        <h2 className="text-[16px] font-semibold">
          {t("mailTemplates.title")}
        </h2>
        <p className="text-ink-soft mt-1 text-[13px]">
          {t("mailTemplates.intro")}
        </p>

        <div className="mt-4 flex flex-col gap-1">
          <label htmlFor="mail-template-locale" className={EYEBROW}>
            {t("mailTemplates.language")}
          </label>
          <select
            id="mail-template-locale"
            className={`${CONTROL} h-9 max-w-[220px]`}
            value={locale}
            onChange={(event) => {
              setLocale(event.target.value as MailLocale);
            }}
          >
            {MAIL_LOCALES.map((option) => (
              <option key={option} value={option}>
                {t(`mailTemplates.locales.${option}`)}
              </option>
            ))}
          </select>
        </div>

        <div className="mt-4">
          <Table>
            <Table.Head>
              <Table.HeaderCell>{t("mailTemplates.message")}</Table.HeaderCell>
              <Table.HeaderCell>
                {t("mailTemplates.fields.subject")}
              </Table.HeaderCell>
              <Table.HeaderCell>{t("mailTemplates.status")}</Table.HeaderCell>
              <Table.HeaderCell srOnly>
                {t("mailTemplates.edit")}
              </Table.HeaderCell>
            </Table.Head>
            <Table.Body>
              {templates.data.kinds.map((summary) => (
                <TemplateRow
                  key={summary.kind}
                  summary={summary}
                  locale={locale}
                  selected={selected === summary.kind}
                  onSelect={() => setSelected(summary.kind)}
                />
              ))}
            </Table.Body>
          </Table>
        </div>
      </Card>

      {selected !== null && detail.isError && (
        <Card className="p-6">
          <p role="alert" className="text-error text-[14px]">
            {t("mailTemplates.loadError", { message: detail.error.message })}
          </p>
        </Card>
      )}
      {selected !== null && detail.isPending && (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        </Card>
      )}
      {selected !== null && detail.data && (
        <TemplateEditor
          key={`${detail.data.kind}/${detail.data.locale}/${detail.data.updatedAt ?? "default"}`}
          slug={slug}
          detail={detail.data}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  );
}
