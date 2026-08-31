import { useEffect, useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import {
  RECIPIENT_CHANNEL,
  SIGNER_KIND,
  SIGNER_STATUS,
  SIGNING_MODE,
  SIGNING_STATUS,
  downloadSignedDocument,
} from "../api/signing";
import type {
  Placement,
  RecipientChannel,
  SignerKind,
  SignerSelection,
  SigningMode,
  SigningRequest,
} from "../api/signing";
import {
  useCreateSigningRequestMutation,
  useInvalidateSigningCredential,
  useLinkSigningCredentialMutation,
  usePendingSigningRequestsQuery,
  useSigningCredentialQuery,
  useSigningRequestQuery,
  useStartSignRequestMutation,
} from "../api/signing.queries";
import { useMeQuery } from "../api/auth.queries";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import type { MemberListEntry } from "../api/organization";
import {
  channelLabel,
  modeLabel,
  signerKindLabel,
  signerStatusLabel,
} from "../lib/signing-labels";
import { alreadyChosen, isEmailish, signerKey } from "../lib/signer-selection";
import { formatBytes } from "../lib/format-bytes";
import {
  MAX_DOCUMENT_BYTES,
  documentIsTooLarge,
} from "../lib/signing-document";
import { placementsIncomplete } from "../lib/placement";
import { toast } from "../lib/toast";
import { Avatar, Button, Card, Icon, Input, Tag, TopBar } from "../ui";
import { SigningHistoryPanel } from "./signing-history";

// The placement editor pulls in pdf.js, which is larger than the rest of the app put
// together, so it is a chunk of its own: nobody who is not placing a signature on a
// document should be made to download a PDF renderer.
const PlacementEditor = React.lazy(async () => ({
  default: (await import("./signing-placement")).PlacementEditor,
}));

const CONFLICT_STATUS = 409;
const TOO_LARGE_STATUS = 413;
const LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "border-line bg-surface text-ink w-full rounded-md border px-3 py-2 text-[13px]";
// The native file input's `file:` button carries no hover/focus affordance, so the
// input is visually hidden and the wrapping label is styled as a button — the same
// pattern the profile, theme and issuer screens use.
const FILE_BUTTON =
  "rounded-yivi border-line-strong bg-surface text-ink hover:bg-surface-3 focus-within:border-ink focus-within:ring-ink/10 inline-flex h-9 cursor-pointer items-center border px-3 text-[13px] font-medium transition-colors focus-within:ring-3";
// The house focus treatment for the wizard's custom (non-ui/) controls: the same
// ring Input and the placement editor use, so every custom target shows focus alike.
const WIZARD_FOCUS =
  "outline-none focus-visible:border-ink focus-visible:ring-ink/20 focus-visible:ring-3";
const MEMBER_PAGE_LIMIT = 200;

type TabKey = "toSign" | "new" | "credential" | "history";
// The "history" tab is admin-only; it is appended to the tab bar only for admins.
const MEMBER_TABS = [
  { key: "toSign", labelKey: "signing.tabToSign" },
  { key: "new", labelKey: "signing.tabNew" },
  { key: "credential", labelKey: "signing.tabCredential" },
] as const;
const HISTORY_TAB = {
  key: "history",
  labelKey: "signing.tabHistory",
} as const;

// The backend's own message for a rejected start (its APIError), or the transport
// error when there is none. Mirrors the pattern used by the other settings screens.
function startError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    const code =
      error.body &&
      typeof error.body === "object" &&
      "code" in error.body &&
      typeof error.body.code === "string"
        ? error.body.code
        : "";
    if (code === "not_configured") return t("signing.notConfigured");
    if (code === "no_credential") return t("signing.noCredentialError");
    if (code === "already_signed") return t("signing.alreadySigned");
    if (code === "not_your_turn") return t("signing.notYourTurn");
    if (code === "sign_in_progress") return t("signing.signInProgress");
  }
  if (error instanceof ApiError && error.status === TOO_LARGE_STATUS) {
    return t("signing.documentTooLarge", {
      size: formatBytes(MAX_DOCUMENT_BYTES),
    });
  }
  // The backend renders an APIError as `{error, code}`, so the human-readable part
  // is `error`; reading `message` here always missed and fell through to the
  // client's own "failed with status …" string.
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "error" in error.body &&
    typeof error.body.error === "string"
  ) {
    return t("signing.startError", { message: error.body.error });
  }
  return t("signing.startError", { message: error.message });
}

function memberName(m: MemberListEntry): string {
  if (m.preferredName && m.preferredName.trim() !== "") return m.preferredName;
  const full = `${m.givenNames} ${m.lastName}`.trim();
  return full !== "" ? full : m.email;
}

export default function Signing(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const [searchParams, setSearchParams] = useSearchParams();
  const invalidateCredential = useInvalidateSigningCredential(slug);
  const credential = useSigningCredentialQuery(slug);
  const isLinked = credential.data != null;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const tabs = isAdmin ? [...MEMBER_TABS, HISTORY_TAB] : MEMBER_TABS;

  const requestedTab = (searchParams.get("tab") as TabKey | null) ?? "toSign";
  // Guard against a non-admin landing on ?tab=history (e.g. a shared link).
  const activeTab: TabKey = tabs.some((tab) => tab.key === requestedTab)
    ? requestedTab
    : "toSign";
  const setTab = (tab: TabKey): void =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (tab === "toSign") next.delete("tab");
        else next.set("tab", tab);
        return next;
      },
      { replace: true },
    );

  // The signing request to track after returning from the wallet ceremony is
  // taken straight from the URL (?request=<id>, appended by the callback).
  const requestId = searchParams.get("request");

  // The callback also appends ?link=ok|failed for the credential-link ceremony.
  // Announce it once, then strip the flag so a refresh does not re-fire the toast.
  const linkResult = searchParams.get("link");
  useEffect(() => {
    if (!linkResult) return;
    if (linkResult === "ok") {
      toast.success(t("signing.linkedToast"));
      invalidateCredential();
    } else {
      toast.error(t("signing.linkFailedToast"));
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("link");
        return next;
      },
      { replace: true },
    );
  }, [linkResult, t, invalidateCredential, setSearchParams]);

  return (
    <>
      <TopBar title={t("signing.title")} subtitle={t("signing.subtitle")} />

      <div
        role="tablist"
        aria-label={t("signing.title")}
        className="border-line bg-surface flex gap-1 border-b px-8"
        onKeyDown={(e) => {
          if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
          e.preventDefault();
          const i = tabs.findIndex((tab) => tab.key === activeTab);
          const delta = e.key === "ArrowRight" ? 1 : -1;
          const nextTab = tabs[(i + delta + tabs.length) % tabs.length];
          setTab(nextTab.key);
          document.getElementById(`signing-tab-${nextTab.key}`)?.focus();
        }}
      >
        {tabs.map((tab) => {
          const active = activeTab === tab.key;
          return (
            <button
              key={tab.key}
              id={`signing-tab-${tab.key}`}
              type="button"
              role="tab"
              aria-selected={active}
              aria-controls="signing-tabpanel"
              tabIndex={active ? 0 : -1}
              onClick={() => setTab(tab.key)}
              className={[
                "h-11 border-b-2 px-3.5 text-[13.5px] transition-colors",
                active
                  ? "border-primary text-ink font-semibold"
                  : "text-ink-soft hover:text-ink border-transparent font-medium",
              ].join(" ")}
            >
              {t(tab.labelKey)}
            </button>
          );
        })}
      </div>

      <div
        id="signing-tabpanel"
        role="tabpanel"
        aria-labelledby={`signing-tab-${activeTab}`}
        className="p-8"
      >
        {activeTab === "history" ? (
          <SigningHistoryPanel slug={slug} enabled={isAdmin} />
        ) : (
          <div
            className={`flex flex-col gap-6 ${
              activeTab === "new" ? "max-w-6xl" : "max-w-3xl"
            }`}
          >
            {requestId && (
              <ActiveRequestCard slug={slug} requestId={requestId} />
            )}

            {activeTab === "toSign" && (
              <ToSignTab
                slug={slug}
                isLinked={isLinked}
                onGoLink={() => setTab("credential")}
              />
            )}
            {activeTab === "new" && (
              <NewRequestTab slug={slug} onCreated={() => setTab("toSign")} />
            )}
            {activeTab === "credential" && (
              <CredentialTab
                slug={slug}
                credential={credential}
                isLinked={isLinked}
              />
            )}
          </div>
        )}
      </div>
    </>
  );
}

// ActiveRequestCard tracks the request the browser just returned from signing,
// polling until it settles and offering the download once complete.
function ActiveRequestCard({
  slug,
  requestId,
}: {
  slug: string;
  requestId: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const me = useMeQuery();
  const request = useSigningRequestQuery(slug, requestId, me.data?.id);

  const onDownload = (): void => {
    if (!request.data) return;
    void downloadSignedDocument(slug, request.data.id, request.data.filename)
      .then(() => toast.success(t("signing.downloadedToast")))
      .catch(() => toast.error(t("signing.downloadError")));
  };

  return (
    <Card className="p-7">
      <h2 className="text-ink text-[15px] font-semibold">
        {t("signing.requestTitle")}
      </h2>
      {request.isPending ? (
        <p className="text-ink-soft mt-3 text-[13px]">{t("common.loading")}</p>
      ) : request.isError ? (
        <p className="text-error mt-3 text-[13px]">
          {t("signing.requestLoadError")}
        </p>
      ) : request.data.status === SIGNING_STATUS.completed ? (
        <div className="mt-3 flex flex-col gap-3">
          <p className="text-ink text-[13px]">
            {t("signing.requestCompleted", { filename: request.data.filename })}
          </p>
          <SignerList request={request.data} />
          <div>
            <Button type="button" onClick={onDownload}>
              {t("signing.downloadButton")}
            </Button>
          </div>
        </div>
      ) : request.data.status === SIGNING_STATUS.failed ? (
        <p className="text-error mt-3 text-[13px]">
          {t("signing.requestFailed", {
            reason: request.data.error || t("signing.requestFailedGeneric"),
          })}
        </p>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          <p className="text-ink-soft text-[13px]">
            {t("signing.requestPending")}
          </p>
          <SignerList request={request.data} />
        </div>
      )}
    </Card>
  );
}

// SignerList renders each signer as a status chip, naming external signees as such —
// who signed from outside the organisation is part of reading a co-signed document.
function SignerList({
  request,
}: {
  request: SigningRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const signers = request.signers ?? [];
  return (
    <div className="flex flex-wrap gap-2">
      {signers.map((s) => (
        <Tag
          key={s.id}
          tone={
            s.status === SIGNER_STATUS.signed
              ? "green"
              : s.status === SIGNER_STATUS.failed
                ? "red"
                : "default"
          }
        >
          {s.name || s.email}
          {s.kind === SIGNER_KIND.external
            ? ` (${t("signing.externalTag")})`
            : ""}
          {" · "}
          {signerStatusLabel(t, s.status)}
        </Tag>
      ))}
    </div>
  );
}

// ToSignTab lists the documents awaiting the current user's signature.
function ToSignTab({
  slug,
  isLinked,
  onGoLink,
}: {
  slug: string;
  isLinked: boolean;
  onGoLink: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const pending = usePendingSigningRequestsQuery(slug);
  const start = useStartSignRequestMutation(slug);
  const [signingId, setSigningId] = useState<string | null>(null);

  const onSign = (id: string): void => {
    setSigningId(id);
    start.mutate(id, {
      onSuccess: (s) => window.location.assign(s.authorizeUrl),
      onError: (error) => {
        setSigningId(null);
        toast.error(startError(error, t));
      },
    });
  };

  const requests = pending.data ?? [];

  return (
    <div className="flex flex-col gap-4">
      {!isLinked && (
        <Card className="p-5">
          <p className="text-ink-soft text-[13px]">
            {t("signing.signNeedsCredential")}{" "}
            <button
              type="button"
              className="text-link underline"
              onClick={onGoLink}
            >
              {t("signing.tabCredential")}
            </button>
          </p>
        </Card>
      )}
      {pending.isPending ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
        </Card>
      ) : pending.isError ? (
        <Card className="p-6">
          <p className="text-error text-[13px]">{t("signing.toSignError")}</p>
        </Card>
      ) : requests.length === 0 ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[13px]">
            {t("signing.toSignEmpty")}
          </p>
        </Card>
      ) : (
        requests.map((req) => (
          <Card
            key={req.id}
            className="flex items-center justify-between gap-4 p-5"
          >
            <div className="min-w-0">
              <p className="text-ink truncate text-[14px] font-medium">
                {req.filename}
              </p>
              <p className="text-ink-soft mt-0.5 text-[12px]">
                {t("signing.requestedBy", { name: req.createdByName })}
                {" · "}
                {modeLabel(t, req.mode)}
              </p>
              <div className="mt-2">
                <SignerList request={req} />
              </div>
            </div>
            <Button
              type="button"
              onClick={() => onSign(req.id)}
              loading={start.isPending && signingId === req.id}
              disabled={!isLinked || (start.isPending && signingId === req.id)}
            >
              {t("signing.signButton")}
            </Button>
          </Card>
        ))
      )}
    </div>
  );
}

// A pill toggle for a small set of mutually exclusive options — the same segmented
// control the design uses for signing order and, in the placement rail, mark kind.
function Segmented<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
  ariaLabel: string;
}): React.JSX.Element {
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      className="bg-surface-3 rounded-yivi inline-flex gap-0.5 p-0.5"
    >
      {options.map((option) => {
        const active = value === option.value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(option.value)}
            className={[
              "h-[30px] rounded-[6px] px-3.5 text-[12.5px] transition-colors",
              WIZARD_FOCUS,
              active
                ? "bg-surface text-ink shadow-card font-semibold"
                : "text-ink-soft hover:text-ink font-medium",
            ].join(" ")}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

// The wizard header rail: a numbered pill per step, connected by short lines, with a
// tick for a completed step and an underline on the current one. A completed step is
// clickable to step back to it; a step ahead of the current one is not reachable.
function WizardStepRail({
  steps,
  current,
  onStep,
}: {
  steps: string[];
  current: number;
  onStep: (index: number) => void;
}): React.JSX.Element {
  return (
    <ol className="flex items-center overflow-x-auto">
      {steps.map((label, index) => {
        const done = index < current;
        const active = index === current;
        return (
          <li key={label} className="flex items-center">
            {index > 0 && (
              <span
                className="bg-line-strong mx-1 h-px w-6 shrink-0 md:w-7"
                aria-hidden="true"
              />
            )}
            <button
              type="button"
              disabled={index > current}
              aria-current={active ? "step" : undefined}
              onClick={() => onStep(index)}
              className={[
                "flex items-center gap-2.5 pb-3.5 whitespace-nowrap",
                WIZARD_FOCUS,
                index > current ? "cursor-default" : "cursor-pointer",
                active ? "shadow-[inset_0_-2px_0_var(--yb-ink)]" : "",
              ].join(" ")}
            >
              <span
                className={[
                  "flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full text-[11px] font-semibold",
                  done
                    ? "bg-success text-white"
                    : active
                      ? "bg-ink text-white"
                      : "border-line-strong text-muted border",
                ].join(" ")}
              >
                {done ? <Icon name="valid" size={12} /> : index + 1}
              </span>
              <span
                className={[
                  "font-display text-[13.5px]",
                  active
                    ? "text-ink font-semibold"
                    : done
                      ? "text-ink-soft font-medium"
                      : "text-muted font-medium",
                ].join(" ")}
              >
                {label}
              </span>
            </button>
          </li>
        );
      })}
    </ol>
  );
}

// A radio card in the delivery step: a target the whole row selects, with a title,
// a short description, and optional trailing content (an "Encrypted" tag).
function ChannelCard({
  selected,
  title,
  description,
  onSelect,
  trailing,
}: {
  selected: boolean;
  title: string;
  description: string;
  onSelect: () => void;
  trailing?: React.ReactNode;
}): React.JSX.Element {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={[
        "rounded-yivi bg-surface flex items-start gap-3 px-3.5 py-3 text-left transition-colors",
        WIZARD_FOCUS,
        selected
          ? "border-ink shadow-card border-2"
          : "border-line-strong hover:bg-surface-3 border",
      ].join(" ")}
    >
      <span
        className={[
          "mt-0.5 h-3.5 w-3.5 shrink-0 rounded-full",
          selected ? "border-ink border-4" : "border-line-strong border",
        ].join(" ")}
        aria-hidden="true"
      />
      <span className="min-w-0">
        <span className="flex items-center gap-2">
          <span className="font-display text-ink text-[13px] font-semibold">
            {title}
          </span>
          {trailing}
        </span>
        <span className="text-ink-soft mt-0.5 block text-[12px]">
          {description}
        </span>
      </span>
    </button>
  );
}

const WIZARD_STEP_COUNT = 4;

// NewRequestTab is the co-signing request wizard: a document is uploaded, its signees
// are chosen and ordered, their visible marks are placed on the pages, and a delivery
// channel is picked — one decision per step, reviewed before the request is sent.
function NewRequestTab({
  slug,
  onCreated,
}: {
  slug: string;
  onCreated: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const members = useOrganizationMembersQuery(
    slug,
    { status: "active", limit: MEMBER_PAGE_LIMIT },
    true,
  );
  const create = useCreateSigningRequestMutation(slug);

  const [step, setStep] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const [fileTooLarge, setFileTooLarge] = useState(false);
  const [signers, setSigners] = useState<SignerSelection[]>([]);
  const [signerKind, setSignerKind] = useState<SignerKind>(
    SIGNER_KIND.internal,
  );
  const [externalName, setExternalName] = useState("");
  const [externalEmail, setExternalEmail] = useState("");
  const [mode, setMode] = useState<SigningMode>(SIGNING_MODE.parallel);
  const [channel, setChannel] = useState<RecipientChannel>(
    RECIPIENT_CHANNEL.none,
  );
  const [recipientAddress, setRecipientAddress] = useState("");
  const [recipientName, setRecipientName] = useState("");
  const [message, setMessage] = useState("");
  const [search, setSearch] = useState("");

  const chosenUserIds = useMemo(
    () =>
      new Set(
        signers
          .filter((s) => s.kind === SIGNER_KIND.internal)
          .map((s) => s.userId),
      ),
    [signers],
  );
  // Members already chosen drop out of the picker: the list is who can still be
  // added, so "add" never silently does nothing.
  const candidates = useMemo(() => {
    const entries = (members.data?.entries ?? []).filter(
      (m): m is MemberListEntry & { userId: string } =>
        m.userId != null && !chosenUserIds.has(m.userId),
    );
    const q = search.trim().toLowerCase();
    if (q === "") return entries;
    return entries.filter(
      (m) =>
        memberName(m).toLowerCase().includes(q) ||
        m.email.toLowerCase().includes(q),
    );
  }, [members.data, search, chosenUserIds]);

  const addMember = (userId: string): void =>
    setSigners((prev) => [
      ...prev,
      { kind: SIGNER_KIND.internal, userId, placements: [] },
    ]);

  const trimmedExternalEmail = externalEmail.trim();
  const canAddExternal =
    isEmailish(trimmedExternalEmail) &&
    !alreadyChosen(signers, trimmedExternalEmail);

  const addExternal = (): void => {
    if (!canAddExternal) return;
    setSigners((prev) => [
      ...prev,
      {
        kind: SIGNER_KIND.external,
        email: trimmedExternalEmail,
        name: externalName.trim(),
        placements: [],
      },
    ]);
    setExternalName("");
    setExternalEmail("");
  };

  const removeSigner = (index: number): void =>
    setSigners((prev) => prev.filter((_, i) => i !== index));

  const recipientNeedsAddress = channel !== RECIPIENT_CHANNEL.none;
  // Placement is optional as a whole, but a request where only some signatures are
  // visible is a half-finished one — the backend would accept it, so it is caught here.
  const incomplete = placementsIncomplete(signers);
  const canSubmit =
    file != null &&
    signers.length > 0 &&
    incomplete.length === 0 &&
    (!recipientNeedsAddress || recipientAddress.trim() !== "");

  const setPlacements = (index: number, placements: Placement[]): void =>
    setSigners((prev) =>
      prev.map((signer, i) =>
        i === index ? { ...signer, placements } : signer,
      ),
    );

  const submit = (): void => {
    if (!file) return;
    create.mutate(
      {
        document: file,
        signers,
        mode,
        recipientChannel: channel,
        recipientAddress: recipientAddress.trim(),
        recipientName: recipientName.trim(),
        message,
      },
      {
        onSuccess: () => {
          toast.success(t("signing.createdToast"));
          onCreated();
        },
        onError: (error) => toast.error(startError(error, t)),
      },
    );
  };

  const nameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const m of members.data?.entries ?? []) {
      if (m.userId) map.set(m.userId, memberName(m));
    }
    return map;
  }, [members.data]);

  const signerLabel = (signer: SignerSelection): string =>
    signer.kind === SIGNER_KIND.internal
      ? (nameById.get(signer.userId) ?? signer.userId)
      : signer.name || signer.email;

  const onFile = (chosen: File | null): void => {
    const tooLarge = chosen !== null && documentIsTooLarge(chosen);
    setFileTooLarge(tooLarge);
    if (!tooLarge) {
      setFile(chosen);
      // Placements are rectangles on the pages of one document, so they mean
      // nothing once a different document is chosen.
      setSigners((prev) =>
        prev.map((signer) => ({ ...signer, placements: [] })),
      );
    }
  };

  // What each step needs before the wizard will advance past it. Placement is
  // optional, so its only gate is that no signature is left half-placed (incomplete).
  const stepReady = [
    file != null && !fileTooLarge,
    signers.length > 0,
    incomplete.length === 0,
    canSubmit,
  ];
  const isLastStep = step === WIZARD_STEP_COUNT - 1;
  const isSequential = mode === SIGNING_MODE.sequential;

  // Someone with no visible mark still signs — but if others do have marks, that is
  // worth flagging before sending, the way the design surfaces it on the review card.
  const anyPlaced = signers.some((s) => s.placements.length > 0);
  const noMarkSigners = anyPlaced
    ? signers.filter((s) => s.placements.length === 0)
    : [];
  const totalMarks = signers.reduce((n, s) => n + s.placements.length, 0);

  const steps = [
    t("signing.steps.document"),
    t("signing.steps.signees"),
    t("signing.steps.placement"),
    t("signing.steps.delivery"),
  ];

  const headerMeta =
    file != null
      ? `${file.name} · ${t("signing.documentMeta", { count: signers.length })}`
      : t("signing.newDescription");

  return (
    <Card className="overflow-hidden p-0">
      {/* Header — title, live document meta, and the step rail */}
      <div className="border-line bg-surface border-b px-6 pt-5">
        <h2 className="font-display text-ink text-[20px] font-semibold">
          {t("signing.newTitle")}
        </h2>
        <p className="text-ink-soft mt-0.5 truncate text-[13px]">
          {headerMeta}
        </p>
        <div className="mt-4">
          <WizardStepRail steps={steps} current={step} onStep={setStep} />
        </div>
      </div>

      {/* Body — the placement step is a full-bleed two-pane canvas, the rest are
          padded single columns. */}
      {step === 2 ? (
        <div>
          {file != null && signers.length > 0 ? (
            <React.Suspense
              fallback={
                <p className="text-ink-soft p-6 text-[13px]">
                  {t("common.loading")}
                </p>
              }
            >
              {/* Keyed on the file so a replaced document remounts the editor with a
                  clean loading state rather than re-deriving one from the old page. */}
              <PlacementEditor
                key={`${file.name}:${file.size}:${file.lastModified}`}
                file={file}
                signers={signers.map((signer) => ({
                  name: signerLabel(signer),
                }))}
                placements={signers.map((signer) => signer.placements)}
                onChange={setPlacements}
              />
            </React.Suspense>
          ) : (
            <p className="text-ink-soft p-6 text-[13px]">
              {t("signing.signersEmpty")}
            </p>
          )}
          {incomplete.length > 0 && (
            <p className="text-error border-line border-t px-6 py-3 text-[13px]">
              {t("signing.placement.incomplete")}
            </p>
          )}
        </div>
      ) : (
        <div className="p-6">
          {step === 0 && (
            <div className="max-w-xl">
              <span className={LABEL}>{t("signing.documentLabel")}</span>
              <p className="text-ink-soft mt-1 text-[12px]">
                {t("signing.documentStepHint")}
              </p>
              <div className="mt-3 flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <label className={FILE_BUTTON}>
                    <input
                      type="file"
                      accept="application/pdf"
                      className="sr-only"
                      onChange={(e) => {
                        onFile(e.target.files?.[0] ?? null);
                        // Reset so picking the same file after removing it fires
                        // onChange again.
                        e.target.value = "";
                      }}
                    />
                    {file
                      ? t("signing.documentReplace")
                      : t("signing.documentChoose")}
                  </label>
                  {file && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setFile(null)}
                    >
                      {t("signing.documentRemove")}
                    </Button>
                  )}
                </div>
                {file ? (
                  <div className="border-line bg-surface-2 rounded-yivi flex items-center gap-3 border px-3.5 py-3">
                    <span className="bg-error-bg text-error font-display inline-flex h-8 w-7 shrink-0 items-center justify-center rounded-[4px] text-[8.5px] font-bold">
                      PDF
                    </span>
                    <span className="text-ink min-w-0 flex-1 truncate text-[13px] font-medium">
                      {file.name}
                    </span>
                    <span className="text-muted shrink-0 font-mono text-[11px]">
                      {formatBytes(file.size)}
                    </span>
                  </div>
                ) : (
                  <span className="text-ink-soft text-[12px]">
                    {t("signing.documentNone")}
                  </span>
                )}
                {fileTooLarge && (
                  <p role="alert" className="text-error text-[12px]">
                    {t("signing.documentTooLarge", {
                      size: formatBytes(MAX_DOCUMENT_BYTES),
                    })}
                  </p>
                )}
              </div>
            </div>
          )}

          {step === 1 && (
            <div className="flex max-w-2xl flex-col gap-6">
              {/* Order */}
              <div>
                <span className={LABEL}>{t("signing.orderLabel")}</span>
                <div className="mt-2">
                  <Segmented
                    ariaLabel={t("signing.orderLabel")}
                    value={mode}
                    onChange={setMode}
                    options={[
                      {
                        value: SIGNING_MODE.parallel,
                        label: modeLabel(t, SIGNING_MODE.parallel),
                      },
                      {
                        value: SIGNING_MODE.sequential,
                        label: modeLabel(t, SIGNING_MODE.sequential),
                      },
                    ]}
                  />
                </div>
                <p className="text-ink-soft mt-2 text-[12px]">
                  {isSequential
                    ? t("signing.sequentialHint")
                    : t("signing.parallelHint")}
                </p>
              </div>

              {/* Chosen signees */}
              <div>
                <span className={LABEL}>{t("signing.signersLabel")}</span>
                <p className="text-ink-soft mt-1 text-[12px]">
                  {t("signing.signersHint")}
                </p>
                {signers.length === 0 ? (
                  <p className="text-ink-soft mt-3 text-[13px]">
                    {t("signing.signersEmpty")}
                  </p>
                ) : (
                  <ul className="mt-3 flex flex-col gap-2">
                    {signers.map((signer, index) => (
                      <li
                        key={signerKey(signer)}
                        className="border-line bg-surface rounded-yivi shadow-card flex items-center gap-3 border px-3.5 py-3"
                      >
                        {isSequential && (
                          <span className="bg-ink font-display inline-flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full text-[11px] font-semibold text-white">
                            {index + 1}
                          </span>
                        )}
                        <Avatar name={signerLabel(signer)} />
                        <div className="min-w-0 flex-1">
                          <div className="font-display text-ink truncate text-[13.5px] font-semibold">
                            {signerLabel(signer)}
                          </div>
                          {signer.kind === SIGNER_KIND.external && (
                            <div className="text-ink-soft truncate font-mono text-[11px]">
                              {signer.email}
                            </div>
                          )}
                        </div>
                        <Tag
                          tone={
                            signer.kind === SIGNER_KIND.external
                              ? "default"
                              : "blue"
                          }
                        >
                          {signerKindLabel(t, signer.kind)}
                        </Tag>
                        <Button
                          variant="ghost"
                          size="sm"
                          icon="close"
                          iconOnly
                          onClick={() => removeSigner(index)}
                          aria-label={t("signing.removeSigner", {
                            name: signerLabel(signer),
                          })}
                        />
                      </li>
                    ))}
                  </ul>
                )}

                {/* Add a signee */}
                <fieldset className="border-line bg-surface-2 rounded-yivi mt-3 border p-4">
                  <legend className="sr-only">
                    {t("signing.addSignerLabel")}
                  </legend>
                  <div className="flex gap-4">
                    {[SIGNER_KIND.internal, SIGNER_KIND.external].map(
                      (kind) => (
                        <label
                          key={kind}
                          className="flex items-center gap-2 text-[13px]"
                        >
                          <input
                            type="radio"
                            name="signerKind"
                            checked={signerKind === kind}
                            onChange={() => setSignerKind(kind)}
                          />
                          <span className="text-ink">
                            {signerKindLabel(t, kind)}
                          </span>
                        </label>
                      ),
                    )}
                  </div>

                  {signerKind === SIGNER_KIND.internal ? (
                    <>
                      <div className="mt-3">
                        <Input
                          value={search}
                          onChange={(e) => setSearch(e.target.value)}
                          placeholder={t("signing.searchMembers")}
                          aria-label={t("signing.searchMembers")}
                        />
                      </div>
                      <div className="border-line bg-surface mt-3 max-h-56 overflow-y-auto rounded-md border">
                        {members.isPending ? (
                          <p className="text-ink-soft p-3 text-[13px]">
                            {t("common.loading")}
                          </p>
                        ) : candidates.length === 0 ? (
                          <p className="text-ink-soft p-3 text-[13px]">
                            {t("signing.noMembers")}
                          </p>
                        ) : (
                          candidates.map((m) => (
                            <div
                              key={m.userId}
                              className="hover:bg-surface-3 flex items-center gap-3 px-3 py-2"
                            >
                              <Avatar name={memberName(m)} size="md" />
                              <span className="text-ink min-w-0 flex-1 truncate text-[13px]">
                                {memberName(m)}
                              </span>
                              <span className="text-ink-soft truncate font-mono text-[11px]">
                                {m.email}
                              </span>
                              <Button
                                variant="secondary"
                                size="sm"
                                icon="add"
                                onClick={() => addMember(m.userId)}
                              >
                                {t("signing.addSigner")}
                              </Button>
                            </div>
                          ))
                        )}
                      </div>
                    </>
                  ) : (
                    <div className="mt-3 flex flex-col gap-2">
                      <p className="text-ink-soft text-[12px]">
                        {t("signing.externalHint")}
                      </p>
                      <Input
                        value={externalName}
                        onChange={(e) => setExternalName(e.target.value)}
                        placeholder={t("signing.externalNamePlaceholder")}
                        aria-label={t("signing.externalNamePlaceholder")}
                      />
                      <Input
                        type="email"
                        value={externalEmail}
                        onChange={(e) => setExternalEmail(e.target.value)}
                        placeholder={t("signing.externalEmailPlaceholder")}
                        aria-label={t("signing.externalEmailPlaceholder")}
                      />
                      <div>
                        <Button
                          type="button"
                          variant="secondary"
                          icon="add"
                          onClick={addExternal}
                          disabled={!canAddExternal}
                        >
                          {t("signing.addSigner")}
                        </Button>
                      </div>
                    </div>
                  )}
                </fieldset>
              </div>
            </div>
          )}

          {step === 3 && (
            <div className="flex flex-col gap-8 lg:flex-row lg:items-start">
              {/* Delivery choices */}
              <div className="min-w-0 flex-1 lg:max-w-xl">
                <h3 className="font-display text-ink text-[16px] font-semibold">
                  {t("signing.deliveryTitle")}
                </h3>
                <p className="text-ink-soft mt-2 text-[12.5px] leading-relaxed text-pretty">
                  {t("signing.deliveryStepHint")}
                </p>

                <div
                  role="radiogroup"
                  aria-label={t("signing.recipientLabel")}
                  className="mt-4 flex flex-col gap-2"
                >
                  <ChannelCard
                    selected={channel === RECIPIENT_CHANNEL.none}
                    onSelect={() => setChannel(RECIPIENT_CHANNEL.none)}
                    title={channelLabel(t, RECIPIENT_CHANNEL.none)}
                    description={t("signing.deliveryNoneDesc")}
                  />
                  <ChannelCard
                    selected={channel === RECIPIENT_CHANNEL.email}
                    onSelect={() => setChannel(RECIPIENT_CHANNEL.email)}
                    title={channelLabel(t, RECIPIENT_CHANNEL.email)}
                    description={t("signing.deliveryEmailDesc")}
                  />
                  <ChannelCard
                    selected={channel === RECIPIENT_CHANNEL.qerds}
                    onSelect={() => setChannel(RECIPIENT_CHANNEL.qerds)}
                    title={channelLabel(t, RECIPIENT_CHANNEL.qerds)}
                    description={t("signing.deliveryQerdsDesc")}
                  />
                </div>

                {recipientNeedsAddress && (
                  <div className="mt-4 flex flex-col gap-3">
                    <Input
                      value={recipientAddress}
                      onChange={(e) => setRecipientAddress(e.target.value)}
                      placeholder={
                        channel === RECIPIENT_CHANNEL.email
                          ? t("signing.recipientEmailPlaceholder")
                          : t("signing.recipientQerdsPlaceholder")
                      }
                      aria-label={
                        channel === RECIPIENT_CHANNEL.email
                          ? t("signing.recipientEmailPlaceholder")
                          : t("signing.recipientQerdsPlaceholder")
                      }
                    />
                    <Input
                      value={recipientName}
                      onChange={(e) => setRecipientName(e.target.value)}
                      placeholder={t("signing.recipientNamePlaceholder")}
                      aria-label={t("signing.recipientNamePlaceholder")}
                    />
                    <textarea
                      className={CONTROL}
                      rows={3}
                      value={message}
                      onChange={(e) => setMessage(e.target.value)}
                      placeholder={t("signing.messagePlaceholder")}
                      aria-label={t("signing.messagePlaceholder")}
                    />
                  </div>
                )}
              </div>

              {/* Review summary */}
              <div className="w-full shrink-0 lg:w-[360px]">
                <Card className="overflow-hidden p-0">
                  <div className="border-line border-b px-4 py-3.5">
                    <div className="text-muted font-mono text-[11px] tracking-wide uppercase">
                      {t("signing.summaryTitle")}
                    </div>
                    <div className="mt-2 flex items-center gap-2.5">
                      <span className="bg-error-bg text-error font-display inline-flex h-8 w-[26px] shrink-0 items-center justify-center rounded-[4px] text-[8.5px] font-bold">
                        PDF
                      </span>
                      <div className="min-w-0">
                        <div className="font-display text-ink truncate text-[12.5px] font-semibold">
                          {file?.name ?? t("signing.documentNone")}
                        </div>
                        <div className="text-ink-soft font-mono text-[10.5px]">
                          {file ? formatBytes(file.size) : ""}
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="border-line flex flex-col gap-2.5 border-b px-4 py-3">
                    {signers.map((signer, index) => (
                      <div
                        key={signerKey(signer)}
                        className="flex items-center gap-2.5"
                      >
                        <Avatar name={signerLabel(signer)} size="md" />
                        <span className="font-display text-ink min-w-0 flex-1 truncate text-[12.5px] font-semibold">
                          {signerLabel(signer)}
                        </span>
                        {isSequential && (
                          <span className="text-muted shrink-0 font-mono text-[10.5px]">
                            #{index + 1}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                  <dl className="flex flex-col gap-1.5 px-4 py-3 text-[12.5px]">
                    <div className="flex justify-between gap-3">
                      <dt className="text-ink-soft">
                        {t("signing.summaryMarksLabel")}
                      </dt>
                      <dd className="text-ink font-semibold">
                        {t("signing.placement.markCount", {
                          count: totalMarks,
                        })}
                      </dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-ink-soft">
                        {t("signing.summaryOrderLabel")}
                      </dt>
                      <dd className="text-ink font-semibold">
                        {modeLabel(t, mode)}
                      </dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-ink-soft">
                        {t("signing.summaryDeliveryLabel")}
                      </dt>
                      <dd className="text-ink font-semibold">
                        {channelLabel(t, channel)}
                      </dd>
                    </div>
                  </dl>
                </Card>

                {noMarkSigners.map((signer) => (
                  <div
                    key={signerKey(signer)}
                    className="bg-warning-bg rounded-yivi mt-3 flex items-center gap-2 px-3 py-2.5"
                  >
                    <Icon
                      name="warning"
                      size={14}
                      className="text-warning-fg shrink-0"
                    />
                    <span className="text-warning-fg text-[12px] font-semibold">
                      {t("signing.noVisibleMarkWarning", {
                        name: signerLabel(signer),
                      })}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Footer — one Back / primary bar shared by every step */}
      <div className="border-line bg-surface-2 flex items-center gap-3 border-t px-6 py-4">
        <Button
          variant="secondary"
          onClick={() => setStep((s) => Math.max(s - 1, 0))}
          disabled={step === 0}
        >
          {t("signing.wizardBack")}
        </Button>
        <div className="flex-1" />
        {isLastStep ? (
          <Button
            onClick={submit}
            loading={create.isPending}
            disabled={!canSubmit}
          >
            {t("signing.sendRequest")}
          </Button>
        ) : (
          <Button
            onClick={() => setStep((s) => s + 1)}
            disabled={!stepReady[step]}
          >
            {t("signing.wizardNext")}
          </Button>
        )}
      </div>
    </Card>
  );
}

// CredentialTab holds the one-time link/relink of the acting user's signing
// credential — configured once, out of the per-document flow.
function CredentialTab({
  slug,
  credential,
  isLinked,
}: {
  slug: string;
  credential: ReturnType<typeof useSigningCredentialQuery>;
  isLinked: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const link = useLinkSigningCredentialMutation(slug);

  const onLink = (): void => {
    link.mutate(undefined, {
      onSuccess: (start) => window.location.assign(start.authorizeUrl),
      onError: (error) => toast.error(startError(error, t)),
    });
  };

  return (
    <Card className="p-7">
      <h2 className="text-ink text-[15px] font-semibold">
        {t("signing.credentialTitle")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("signing.credentialDescription")}
      </p>

      {credential.isPending ? (
        <p className="text-ink-soft mt-4 text-[13px]">{t("common.loading")}</p>
      ) : credential.isError ? (
        <p className="text-error mt-4 text-[13px]">
          {t("signing.credentialLoadError")}
        </p>
      ) : credential.data != null ? (
        <dl className="mt-4 flex flex-col gap-2">
          <div>
            <dt className={LABEL}>{t("signing.credentialIdLabel")}</dt>
            <dd className="text-ink font-mono text-[12px] break-all">
              {credential.data.credentialId}
            </dd>
          </div>
          <div>
            <dt className={LABEL}>{t("signing.keyAlgoLabel")}</dt>
            <dd className="text-ink text-[13px]">{credential.data.keyAlgo}</dd>
          </div>
        </dl>
      ) : (
        <p className="text-ink-soft mt-4 text-[13px]">
          {t("signing.notLinked")}
        </p>
      )}

      <div className="mt-5">
        <Button type="button" onClick={onLink} loading={link.isPending}>
          {isLinked ? t("signing.relinkButton") : t("signing.linkButton")}
        </Button>
      </div>
      <p className="text-ink-soft mt-3 text-[12px]">
        {t("signing.walletHint")}
      </p>
    </Card>
  );
}
