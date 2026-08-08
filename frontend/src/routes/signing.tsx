import { useEffect, useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import {
  RECIPIENT_CHANNEL,
  SIGNER_STATUS,
  SIGNING_MODE,
  SIGNING_STATUS,
  downloadSignedDocument,
} from "../api/signing";
import type {
  RecipientChannel,
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
import { useOrganizationMembersQuery } from "../api/organization.queries";
import type { MemberListEntry } from "../api/organization";
import { modeLabel, signerStatusLabel } from "../lib/signing-labels";
import { toast } from "../lib/toast";
import { Button, Card, Input, Tag, TopBar } from "../ui";

const CONFLICT_STATUS = 409;
const LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "border-line bg-surface text-ink w-full rounded-md border px-3 py-2 text-[13px]";
const MEMBER_PAGE_LIMIT = 200;

type TabKey = "toSign" | "new" | "credential";
const TABS = [
  { key: "toSign", labelKey: "signing.tabToSign" },
  { key: "new", labelKey: "signing.tabNew" },
  { key: "credential", labelKey: "signing.tabCredential" },
] as const;

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
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "message" in error.body &&
    typeof error.body.message === "string"
  ) {
    return t("signing.startError", { message: error.body.message });
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

  const activeTab = (searchParams.get("tab") as TabKey | null) ?? "toSign";
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

      <div className="p-8">
        <div className="flex max-w-3xl flex-col gap-6">
          {requestId && <ActiveRequestCard slug={slug} requestId={requestId} />}

          <div className="border-line flex gap-1 border-b">
            {TABS.map((tab) => (
              <button
                key={tab.key}
                type="button"
                onClick={() => setTab(tab.key)}
                className={[
                  "-mb-px border-b-2 px-4 py-2.5 text-[13px]",
                  activeTab === tab.key
                    ? "border-primary text-ink font-semibold"
                    : "text-ink-soft border-transparent",
                ].join(" ")}
              >
                {t(tab.labelKey)}
              </button>
            ))}
          </div>

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
  const request = useSigningRequestQuery(slug, requestId);

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

// SignerList renders each signer as a status chip.
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
          key={s.userId}
          tone={
            s.status === SIGNER_STATUS.signed
              ? "green"
              : s.status === SIGNER_STATUS.failed
                ? "red"
                : "default"
          }
        >
          {s.name || s.email}
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

// NewRequestTab is the co-signing request form.
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

  const [file, setFile] = useState<File | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [mode, setMode] = useState<SigningMode>(SIGNING_MODE.parallel);
  const [channel, setChannel] = useState<RecipientChannel>(
    RECIPIENT_CHANNEL.none,
  );
  const [recipientAddress, setRecipientAddress] = useState("");
  const [recipientName, setRecipientName] = useState("");
  const [message, setMessage] = useState("");
  const [search, setSearch] = useState("");

  const candidates = useMemo(() => {
    const entries = (members.data?.entries ?? []).filter(
      (m): m is MemberListEntry & { userId: string } => m.userId != null,
    );
    const q = search.trim().toLowerCase();
    if (q === "") return entries;
    return entries.filter(
      (m) =>
        memberName(m).toLowerCase().includes(q) ||
        m.email.toLowerCase().includes(q),
    );
  }, [members.data, search]);

  const toggle = (userId: string): void =>
    setSelected((prev) =>
      prev.includes(userId)
        ? prev.filter((id) => id !== userId)
        : [...prev, userId],
    );

  const recipientNeedsAddress = channel !== RECIPIENT_CHANNEL.none;
  const canSubmit =
    file != null &&
    selected.length > 0 &&
    (!recipientNeedsAddress || recipientAddress.trim() !== "");

  const onSubmit = (event: React.FormEvent): void => {
    event.preventDefault();
    if (!file) return;
    create.mutate(
      {
        document: file,
        signerIds: selected,
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

  return (
    <Card className="p-7">
      <h2 className="text-ink text-[15px] font-semibold">
        {t("signing.newTitle")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("signing.newDescription")}
      </p>

      <form className="mt-5 flex flex-col gap-5" onSubmit={onSubmit}>
        {/* Document */}
        <div>
          <label className={LABEL}>{t("signing.documentLabel")}</label>
          <input
            type="file"
            accept="application/pdf"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            className="text-ink file:bg-surface-2 mt-2 block text-[13px] file:mr-3 file:rounded-md file:border-0 file:px-3 file:py-1.5 file:text-[13px] file:font-medium"
          />
        </div>

        {/* Signers */}
        <div>
          <label className={LABEL}>{t("signing.signersLabel")}</label>
          <p className="text-ink-soft mt-1 text-[12px]">
            {t("signing.signersHint")}
          </p>
          <div className="mt-2">
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("signing.searchMembers")}
            />
          </div>
          <div className="border-line mt-2 max-h-56 overflow-y-auto rounded-md border">
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
                <label
                  key={m.userId}
                  className="hover:bg-surface-2 flex cursor-pointer items-center gap-3 px-3 py-2"
                >
                  <input
                    type="checkbox"
                    checked={selected.includes(m.userId)}
                    onChange={() => toggle(m.userId)}
                  />
                  <span className="text-ink text-[13px]">{memberName(m)}</span>
                  <span className="text-ink-soft text-[12px]">{m.email}</span>
                </label>
              ))
            )}
          </div>
          {selected.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-2">
              {selected.map((id, i) => (
                <Tag key={id} tone="default">
                  {mode === SIGNING_MODE.sequential ? `${i + 1}. ` : ""}
                  {nameById.get(id) ?? id}
                </Tag>
              ))}
            </div>
          )}
        </div>

        {/* Mode */}
        <div>
          <label className={LABEL}>{t("signing.orderLabel")}</label>
          <div className="mt-2 flex gap-4">
            {[SIGNING_MODE.parallel, SIGNING_MODE.sequential].map((m) => (
              <label key={m} className="flex items-center gap-2 text-[13px]">
                <input
                  type="radio"
                  name="mode"
                  checked={mode === m}
                  onChange={() => setMode(m)}
                />
                <span className="text-ink">{modeLabel(t, m)}</span>
              </label>
            ))}
          </div>
          <p className="text-ink-soft mt-1 text-[12px]">
            {mode === SIGNING_MODE.sequential
              ? t("signing.sequentialHint")
              : t("signing.parallelHint")}
          </p>
        </div>

        {/* Recipient */}
        <div>
          <label className={LABEL}>{t("signing.recipientLabel")}</label>
          <p className="text-ink-soft mt-1 text-[12px]">
            {t("signing.recipientHint")}
          </p>
          <select
            className={`${CONTROL} mt-2 h-9`}
            value={channel}
            onChange={(e) => setChannel(e.target.value as RecipientChannel)}
          >
            <option value={RECIPIENT_CHANNEL.none}>
              {t("signing.channel.none")}
            </option>
            <option value={RECIPIENT_CHANNEL.email}>
              {t("signing.channel.email")}
            </option>
            <option value={RECIPIENT_CHANNEL.qerds}>
              {t("signing.channel.qerds")}
            </option>
          </select>

          {recipientNeedsAddress && (
            <div className="mt-3 flex flex-col gap-3">
              <Input
                value={recipientAddress}
                onChange={(e) => setRecipientAddress(e.target.value)}
                placeholder={
                  channel === RECIPIENT_CHANNEL.email
                    ? t("signing.recipientEmailPlaceholder")
                    : t("signing.recipientQerdsPlaceholder")
                }
              />
              <Input
                value={recipientName}
                onChange={(e) => setRecipientName(e.target.value)}
                placeholder={t("signing.recipientNamePlaceholder")}
              />
              <textarea
                className={CONTROL}
                rows={3}
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder={t("signing.messagePlaceholder")}
              />
            </div>
          )}
        </div>

        <div>
          <Button
            type="submit"
            loading={create.isPending}
            disabled={!canSubmit}
          >
            {t("signing.createButton")}
          </Button>
        </div>
      </form>
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
