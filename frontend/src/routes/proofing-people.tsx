import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useExternalRequestsQuery,
  useProofingRequestEventsQuery,
  useProofingFlowsQuery,
  useProofingRequestResultQuery,
  useProofingSettingsQuery,
} from "../api/identity-proofing.queries";
import type {
  ProofingFlow,
  ProofingImage,
  ProofingRequest,
  ProofingResult,
  ProofingSettings,
} from "../api/identity-proofing";
import { isDataRequest } from "../api/identity-proofing";
import type { UseQueryResult } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import {
  AUDIT_TONE_CLASSES,
  auditActionLabel,
  auditVisual,
} from "../lib/audit-event";
import { useWhenFormatter } from "../lib/format-when";
import { reviewPath } from "../lib/proofing-reviews";
import { legalPeriodLabel } from "../lib/legal-retention";
import {
  addressVerificationNote,
  assuranceLevelLabel,
  flowPurgeDays,
  proofingErrorMessage,
  proofingMethodLabel,
  proofingRejectionReason,
  requestSubject,
  sessionEventDetail,
  SESSION_FILTERS,
  formatDuration,
  listedCount,
  matchesSessionFilter,
  peopleRetentionOf,
  requestFlows,
  requestListCapped,
  requestedBy,
  sessionDurationSeconds,
  sessionFilterCounts,
  shortRequestId,
  imageSource,
  isCheckOutcome,
  showsSessionIdentity,
  timelineActor,
  ageOverFact,
} from "../lib/identity-proofing";
import type { SessionFilter } from "../lib/identity-proofing";
import { Card, Icon, Select, StatusText, Table, TopBar } from "../ui";
import { ResultText } from "./proofing-ui";
import { DataRequestNote } from "./data-request-review";
import { PRIMARY_LINK, SECONDARY_LINK } from "./proofing-review-parts";
import { VerifyPerson } from "./proofing-send";

const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const LABEL = "text-ink text-[13px] font-semibold";
// Person, result, app, started and the details button; the flow and who
// requested it come on top.
const BASE_COLUMNS = 5;
const OPEN_ICON_SIZE = 18;
const TIMELINE_ICON_SIZE = 14;
// A face match score is 0 to 1; shown as a percentage.
const PERCENT = 100;

// Whose sessions the list shows: an admin sees every one, a member their own.
type SessionScope = "all" | "own";

// Where the list sits: on one flow's People tab, or across every flow.
type PeopleScope = "flow" | "all";

// How long the checks' personal data is kept: the legal retention when the org
// has one, else the flow's retention (the org's, across every flow).
function retentionNote(
  settings: ProofingSettings,
  scope: SessionScope,
  flow: ProofingFlow | undefined,
  t: TFunction,
): string {
  if (settings.legalRetention && !isDataRequest(flow?.kind)) {
    const period = legalPeriodLabel(settings.legalRetention.months, t);
    return scope === "all"
      ? t("identityProofing.sessions.retentionLegal", { period })
      : t("identityProofing.sessions.retentionOwnLegal", { period });
  }
  const count = flow
    ? flowPurgeDays(flow, settings)
    : settings.dataRetentionDays;
  return scope === "all"
    ? t("identityProofing.sessions.retention", { count })
    : t("identityProofing.sessions.retentionOwn", { count });
}

// Everyone the org sent a check to, on any flow. A member sees the ones they
// sent, an admin every one; an admin also decides the ones waiting for review.
export default function ProofingPeople(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  return (
    <>
      <TopBar
        title={t("identityProofing.people.title")}
        subtitle={t("identityProofing.people.subtitle")}
        actions={<VerifyPerson slug={slug} isAdmin={isAdmin} />}
      />
      <div className="flex flex-col gap-4 p-4 sm:p-8">
        <PeopleList slug={slug} isAdmin={isAdmin} />
      </div>
    </>
  );
}

// The external people checks were sent to (an admin's; a member's own),
// filterable by outcome, and by flow unless the list is one flow's. A row opens
// to its details: who sent it, and why it failed; ?session= opens one.
export function PeopleList({
  slug,
  isAdmin,
  flow,
}: {
  slug: string;
  isAdmin: boolean;
  flow?: ProofingFlow;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [flowFilter, setFlowFilter] = useState("");
  const scope: PeopleScope = flow ? "flow" : "all";
  const flowId = flow?.id ?? flowFilter;
  // The backend narrows to the flow before its cap, so a quiet flow on a busy
  // org lists its own people; every flow's list names what the filter offers.
  const requests = useExternalRequestsQuery(slug, flowId);
  const everyone = useExternalRequestsQuery(slug, "", scope === "all");
  // A member reads the flows offered to them; another flow's retention is
  // then not stated.
  const flows = useProofingFlowsQuery(
    slug,
    scope === "all" && flowFilter !== "",
  );
  const settings = useProofingSettingsQuery(slug);
  const formatWhen = useWhenFormatter();
  const [searchParams] = useSearchParams();
  const [filter, setFilter] = useState<SessionFilter>("all");
  const [open, setOpen] = useState<string | null>(searchParams.get("session"));
  const columns = BASE_COLUMNS + (scope === "all" ? 1 : 0) + (isAdmin ? 1 : 0);
  const onFlow = requests.data ?? [];
  const capped = requestListCapped(onFlow);
  const counts = sessionFilterCounts(onFlow);
  const shown = onFlow.filter((r) => matchesSessionFilter(r.status, filter));
  const retentionOf = flow
    ? ({ kind: "flow", flow } as const)
    : peopleRetentionOf(flowFilter, flows.data);

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row sm:flex-wrap sm:gap-x-4">
          {scope === "all" && (
            <div className="flex items-center gap-2.5">
              <label htmlFor="people-flow-filter" className={LABEL}>
                {t("identityProofing.requests.flow")}
              </label>
              <Select
                id="people-flow-filter"
                value={flowFilter}
                onChange={(event) => setFlowFilter(event.target.value)}
                className="min-w-0 flex-1 sm:w-64 sm:flex-none"
              >
                <option value="">
                  {t("identityProofing.people.allFlows")}
                </option>
                {requestFlows(everyone.data ?? []).map((option) => (
                  <option key={option.id} value={option.id}>
                    {option.name}
                  </option>
                ))}
              </Select>
            </div>
          )}
          <div className="flex items-center gap-2.5">
            <label htmlFor="people-result-filter" className={LABEL}>
              {t("identityProofing.sessions.result")}
            </label>
            <Select
              id="people-result-filter"
              value={filter}
              onChange={(event) =>
                setFilter(
                  SESSION_FILTERS.find((key) => key === event.target.value) ??
                    "all",
                )
              }
              className="min-w-0 flex-1 sm:w-56 sm:flex-none"
            >
              {SESSION_FILTERS.map((key) => (
                <option key={key} value={key}>
                  {t(`identityProofing.sessions.filters.${key}`, {
                    number: listedCount(counts[key], capped, t),
                  })}
                </option>
              ))}
            </Select>
          </div>
        </div>
        {settings.data && retentionOf.kind !== "unknown" && (
          <p className="text-muted text-[12px]">
            {retentionNote(
              settings.data,
              isAdmin ? "all" : "own",
              retentionOf.kind === "flow" ? retentionOf.flow : undefined,
              t,
            )}
          </p>
        )}
      </div>
      <Card>
        <Table>
          <Table.Head>
            <Table.HeaderCell scope="col">
              {t("identityProofing.requests.subject")}
            </Table.HeaderCell>
            {scope === "all" && (
              <Table.HeaderCell scope="col">
                {t("identityProofing.requests.flow")}
              </Table.HeaderCell>
            )}
            <Table.HeaderCell scope="col">
              {t("identityProofing.sessions.result")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofing.sessions.method")}
            </Table.HeaderCell>
            {isAdmin && (
              <Table.HeaderCell scope="col">
                {t("identityProofing.requests.requestedBy")}
              </Table.HeaderCell>
            )}
            <Table.HeaderCell scope="col">
              {t("identityProofing.sessions.started")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col" srOnly>
              {t("identityProofing.people.details")}
            </Table.HeaderCell>
          </Table.Head>
          <Table.Body>
            {requests.isPending && (
              <Table.State colSpan={columns}>{t("common.loading")}</Table.State>
            )}
            {requests.isError && (
              <Table.State colSpan={columns}>
                {proofingErrorMessage(requests.error, t)}
              </Table.State>
            )}
            {requests.isSuccess && shown.length === 0 && (
              <Table.State colSpan={columns}>
                {t("identityProofing.requests.empty")}
              </Table.State>
            )}
            {requests.isSuccess &&
              shown.map((request) => (
                <SessionRow
                  key={request.id}
                  slug={slug}
                  request={request}
                  isAdmin={isAdmin}
                  scope={scope}
                  columns={columns}
                  expanded={open === request.id}
                  onToggle={() =>
                    setOpen((current) =>
                      current === request.id ? null : request.id,
                    )
                  }
                  formatWhen={formatWhen}
                />
              ))}
          </Table.Body>
        </Table>
      </Card>
    </>
  );
}

function SessionRow({
  slug,
  request,
  isAdmin,
  scope,
  columns,
  expanded,
  onToggle,
  formatWhen,
}: {
  slug: string;
  request: ProofingRequest;
  isAdmin: boolean;
  scope: PeopleScope;
  columns: number;
  expanded: boolean;
  onToggle: () => void;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const subject = requestSubject(request);
  const duration = sessionDurationSeconds(request);
  const detailsId = `session-details-${request.id}`;
  const dataRequest = isDataRequest(request.flowKind);
  // A session in review shows who it is and what the checks found: that is
  // what the reviewer decides on (a data request: who asks). A purged session
  // has no identity left to read, so nothing is fetched.
  const showIdentity = showsSessionIdentity(request, isAdmin);
  const result = useProofingRequestResultQuery(
    slug,
    request.id,
    expanded && showIdentity,
  );

  return (
    <>
      <Table.Row
        className="hover:bg-surface-2 cursor-pointer transition-colors"
        onClick={onToggle}
      >
        <Table.Cell>
          {subject.name ? (
            <div
              className="max-w-56 truncate font-semibold"
              title={subject.name}
            >
              {subject.name}
            </div>
          ) : (
            <div className="text-muted italic">
              {request.purgedAt
                ? t("identityProofing.sessions.purgedSubject")
                : t("identityProofing.requests.noName")}
            </div>
          )}
          {subject.verifiedAs && (
            <div className="text-[12px]">
              {t("identityProofing.requests.verifiedAs", {
                name: subject.verifiedAs,
              })}
            </div>
          )}
          <div className="text-ink-soft font-mono text-[11.5px]">
            {shortRequestId(request.id)}
          </div>
        </Table.Cell>
        {scope === "all" && <Table.Cell>{request.flowName}</Table.Cell>}
        <Table.Cell>
          <ResultText request={request} compact />
          <AddressNote request={request} />
          <DiplomaNote request={request} />
          <DataRequestNote request={request} />
        </Table.Cell>
        <Table.Cell className="text-ink-soft whitespace-nowrap">
          {proofingMethodLabel(request.method, t)}
        </Table.Cell>
        {isAdmin && <Table.Cell>{requestedBy(request, t) || "—"}</Table.Cell>}
        <Table.Cell className="whitespace-nowrap">
          {formatWhen(request.createdAt)}
        </Table.Cell>
        <Table.Cell className="w-10 text-right">
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={detailsId}
            aria-label={t("identityProofing.sessions.details", {
              id: shortRequestId(request.id),
            })}
            onClick={(event) => {
              event.stopPropagation();
              onToggle();
            }}
            className="text-muted hover:text-ink"
          >
            <Icon
              name={expanded ? "chevron_down" : "chevron_right"}
              size={OPEN_ICON_SIZE}
            />
          </button>
        </Table.Cell>
      </Table.Row>
      {expanded && (
        <tr id={detailsId}>
          <td
            colSpan={columns}
            className="border-line bg-surface-2 border-b px-5 py-4"
          >
            <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
              <div className="flex min-w-0 flex-col gap-4">
                <dl className="grid content-start gap-x-8 gap-y-2 text-[13px] sm:grid-cols-[auto_1fr]">
                  <dt className="text-muted">
                    {t("identityProofing.sessions.method")}
                  </dt>
                  <dd>{proofingMethodLabel(request.method, t)}</dd>
                  <dt className="text-muted">
                    {t("identityProofing.send.email")}
                  </dt>
                  <dd>{request.subjectEmail || "—"}</dd>
                  {isAdmin && (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.requests.requestedBy")}
                      </dt>
                      <dd>{requestedBy(request, t) || "—"}</dd>
                    </>
                  )}
                  <dt className="text-muted">
                    {t("identityProofing.sessions.flowVersion")}
                  </dt>
                  <dd>
                    {request.flowVersion === undefined
                      ? "—"
                      : t("identityProofingFlows.versionShort", {
                          version: request.flowVersion,
                        })}
                  </dd>
                  <dt className="text-muted">
                    {t("identityProofing.requests.assurance")}
                  </dt>
                  <dd>
                    {request.eidasLevel
                      ? assuranceLevelLabel(request.eidasLevel, t)
                      : "—"}
                  </dd>
                  <dt className="text-muted">
                    {t("identityProofing.sessions.fullId")}
                  </dt>
                  <dd className="font-mono text-[12px] break-all">
                    {request.id}
                  </dd>
                  {request.expectedSubject && (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.sessions.expectedSubject")}
                      </dt>
                      <dd>
                        {t("identityProofing.sessions.expectedSubjectValue")}
                      </dd>
                    </>
                  )}
                  {request.errorCode && (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.sessions.reason")}
                      </dt>
                      <dd>{proofingRejectionReason(request.errorCode, t)}</dd>
                    </>
                  )}
                  {request.completedAt && (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.sessions.completed")}
                      </dt>
                      <dd>{formatWhen(request.completedAt)}</dd>
                    </>
                  )}
                  {duration !== undefined && (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.sessions.duration")}
                      </dt>
                      <dd>{formatDuration(duration)}</dd>
                    </>
                  )}
                  {request.purgedAt ? (
                    <>
                      <dt className="text-muted">
                        {t("identityProofing.sessions.purged")}
                      </dt>
                      <dd>{formatWhen(request.purgedAt)}</dd>
                    </>
                  ) : (
                    request.purgeAt && (
                      <>
                        <dt className="text-muted">
                          {t("identityProofing.sessions.purgeAt")}
                        </dt>
                        <dd>{formatWhen(request.purgeAt)}</dd>
                      </>
                    )
                  )}
                  {showIdentity && <IdentityRows result={result} />}
                </dl>
                {showIdentity && result.data && (
                  // Keyed by the session, so a photo revealed in one session is
                  // blurred again in the next.
                  <IdentityPhotos key={request.id} result={result.data} />
                )}
                {showIdentity && (
                  <p className={HINT}>
                    {t("identityProofing.sessions.identity.audited")}
                  </p>
                )}
                {request.addressMode !== "off" && (
                  <SessionAddress request={request} />
                )}
                {request.diplomaMode !== "off" && (
                  <SessionDiplomas request={request} />
                )}
              </div>
              <SessionTimeline slug={slug} request={request} />
            </div>
            {isAdmin && (dataRequest || request.status === "needs_review") && (
              <div className="border-line mt-4 border-t pt-4">
                <Link
                  to={reviewPath(slug, request.id)}
                  className={
                    request.status === "needs_review"
                      ? PRIMARY_LINK
                      : SECONDARY_LINK
                  }
                >
                  {t(
                    request.status === "needs_review"
                      ? "identityProofing.reviews.review"
                      : "identityProofing.reviews.view",
                  )}
                </Link>
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

// For an approved session that requires the address, that none was entered.
function AddressNote({
  request,
}: Readonly<{
  request: ProofingRequest;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (
    request.addressMode === "required" &&
    request.status === "approved" &&
    request.address === undefined &&
    request.purgedAt === undefined
  ) {
    return (
      <div className="text-[12px]">
        <StatusText tone="amber">
          {t("identityProofing.sessions.address.missing")}
        </StatusText>
      </div>
    );
  }
  return null;
}

// The postal address the subject entered, and how it was checked.
function SessionAddress({
  request,
}: Readonly<{
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const a = request.address;
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-ink text-[13.5px] font-bold">
        {t("identityProofing.sessions.address.title")}
      </h3>
      {a === undefined ? (
        <p className={HINT}>{t("identityProofing.sessions.address.none")}</p>
      ) : (
        <address className="border-line text-ink rounded-lg border px-3 py-2 text-[13px] not-italic">
          {`${a.street} ${a.houseNumber}`}
          <br />
          {`${a.postalCode} ${a.city}`}
          <br />
          {a.country}
        </address>
      )}
      <p className={HINT}>{addressVerificationNote(a?.verification, t)}</p>
    </div>
  );
}

// Whether a session that asks for diplomas holds any: how many, or, for an
// approved one that requires them, that none came.
function DiplomaNote({
  request,
}: {
  request: ProofingRequest;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  if (request.diplomas.length > 0) {
    return (
      <div className={HINT}>
        {t("identityProofing.sessions.diplomas.tag", {
          count: request.diplomas.length,
        })}
      </div>
    );
  }
  if (request.diplomaMode === "required" && request.status === "approved") {
    return (
      <div className="text-[12px]">
        <StatusText tone="amber">
          {t("identityProofing.sessions.diplomas.missing")}
        </StatusText>
      </div>
    );
  }
  return null;
}

// The DUO diploma extracts a session holds, each as DUO printed it, with the
// number duo.nl/diplomacontrole checks.
function SessionDiplomas({
  request,
}: {
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-ink text-[13.5px] font-bold">
        {t("identityProofing.sessions.diplomas.title")}
      </h3>
      {request.diplomas.length === 0 ? (
        <p className={HINT}>{t("identityProofing.sessions.diplomas.none")}</p>
      ) : (
        <ul className="border-line divide-y rounded-lg border">
          {request.diplomas.map((d) => (
            <li
              key={d.documentNumber}
              className="flex flex-col gap-0.5 px-3 py-2"
            >
              <span className="text-ink text-[13px] font-semibold">
                {d.qualification}
              </span>
              <span className={HINT}>
                {[
                  d.documentType,
                  d.institution,
                  `${d.placeOfIssue} ${d.dateAwarded}`,
                  d.nlqfLevel
                    ? t("identityProofing.diplomas.level", {
                        level: d.nlqfLevel,
                      })
                    : undefined,
                ]
                  .filter((part) => part !== undefined && part.trim() !== "")
                  .join(" · ")}
              </span>
              <span className="text-muted font-mono text-[11.5px]">
                {t("identityProofing.sessions.diplomas.number", {
                  number: d.documentNumber,
                })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// The checks the engine reports, in words; an unknown value shows as it is.
// A settled session's verified identity, as rows of its detail list: read
// from the engine when an admin opens the row, audited each time.
function IdentityRows({
  result,
}: {
  result: UseQueryResult<ProofingResult, Error>;
}): React.JSX.Element {
  const { t } = useTranslation();
  const check = (value: string | undefined): string => {
    if (value === undefined || value === "") {
      return "—";
    }
    return isCheckOutcome(value)
      ? t(`identityProofing.sessions.identity.checks.${value}`)
      : value;
  };

  if (result.isPending) {
    return (
      <>
        <dt className="text-muted">
          {t("identityProofing.sessions.identity.name")}
        </dt>
        <dd className="text-ink-soft">{t("common.loading")}</dd>
      </>
    );
  }
  if (result.isError) {
    return (
      <dd className={`${ERROR} sm:col-span-2`}>
        {proofingErrorMessage(result.error, t)}
      </dd>
    );
  }
  const { identity } = result.data;
  const ageOver = ageOverFact(identity, t);
  const evidence = result.data.evidence.at(0);
  return (
    <>
      {identity ? (
        <>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.name")}
          </dt>
          <dd>
            {[identity.givenName, identity.familyName]
              .filter(Boolean)
              .join(" ") || "—"}
          </dd>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.birthDate")}
          </dt>
          <dd>{identity.birthDate || "—"}</dd>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.nationality")}
          </dt>
          <dd>{identity.nationality || "—"}</dd>
          {ageOver && (
            <>
              <dt className="text-muted">{ageOver.label}</dt>
              <dd>{ageOver.value}</dd>
            </>
          )}
        </>
      ) : (
        <dd className="text-ink-soft sm:col-span-2">
          {t("identityProofing.sessions.identity.none")}
        </dd>
      )}
      {evidence && (
        <>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.document")}
          </dt>
          <dd>
            {evidence.type === "reference_photo"
              ? t("identityProofing.sessions.identity.noDocument")
              : [evidence.documentType, evidence.issuingState]
                  .filter(Boolean)
                  .join(" · ") || evidence.type}
          </dd>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.passiveAuth")}
          </dt>
          <dd>{check(evidence.passiveAuth)}</dd>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.faceMatch")}
          </dt>
          <dd>
            {evidence.faceMatch === undefined
              ? "—"
              : `${Math.round(evidence.faceMatch * PERCENT)}%`}
          </dd>
          <dt className="text-muted">
            {t("identityProofing.sessions.identity.liveness")}
          </dt>
          <dd>{check(evidence.liveness)}</dd>
        </>
      )}
    </>
  );
}

// The document's photo (read off the chip over NFC, or the disclosed
// credential's), or the sender's own photo for a flow without the chip,
// beside the live selfie matched against it, and the photos of
// the document's front and back; only an approval carries them, only when
// the flow requested them, and the face photos only when it keeps them. Each
// is blurred until a member reveals it.
function IdentityPhotos({
  result,
}: {
  result: ProofingResult;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const photos = [
    { key: "photo", image: result.photo },
    { key: "referencePhoto", image: result.referencePhoto },
    { key: "selfie", image: result.selfie },
    { key: "documentImage", image: result.documentImage },
    { key: "documentImageBack", image: result.documentImageBack },
  ] as const;
  if (photos.every(({ image }) => !image)) return null;
  return (
    <div className="flex flex-wrap gap-4">
      {photos.map(
        ({ key, image }) =>
          image && (
            <RevealablePhoto
              key={key}
              image={image}
              label={t(`identityProofing.sessions.identity.${key}`)}
            />
          ),
      )}
    </div>
  );
}

const REVEAL_ICON_SIZE = 16;

// A photo blurred until a member reveals it. It is in the page already (and
// opening the session is audited); the blur keeps a face or a document from
// being seen in passing, on a shared screen or over a shoulder.
function RevealablePhoto({
  image,
  label,
}: {
  image: ProofingImage;
  label: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [revealed, setRevealed] = useState(false);
  return (
    <figure className="flex flex-col gap-1.5">
      <div className="border-line relative h-40 overflow-hidden rounded-md border">
        <img
          src={imageSource(image)}
          alt={label}
          className={`h-full w-auto object-cover transition-[filter] motion-reduce:transition-none ${revealed ? "" : "scale-110 blur-xl"}`}
        />
        {!revealed && (
          <button
            type="button"
            onClick={() => setRevealed(true)}
            aria-label={t("identityProofing.sessions.identity.revealPhoto", {
              name: label,
            })}
            className="focus-visible:ring-ink/30 absolute inset-0 flex items-center justify-center outline-none focus-visible:ring-3"
          >
            <span className="bg-surface/90 text-ink inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-[12.5px] font-semibold shadow-sm">
              <Icon name="view" size={REVEAL_ICON_SIZE} />
              {t("identityProofing.sessions.identity.reveal")}
            </span>
          </button>
        )}
      </div>
      <figcaption className="text-muted flex items-center gap-2 text-[12px]">
        {label}
        {revealed && (
          <button
            type="button"
            onClick={() => setRevealed(false)}
            className="text-link font-semibold hover:underline"
          >
            {t("identityProofing.sessions.identity.conceal")}
          </button>
        )}
      </figcaption>
    </figure>
  );
}

// A session's timeline: every audit event about it, oldest first, with who
// acted (a member, the system, or the API) and what it added.
function SessionTimeline({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const events = useProofingRequestEventsQuery(slug, request);
  const formatWhen = useWhenFormatter();

  return (
    <section aria-labelledby={`timeline-${request.id}`}>
      <h3
        id={`timeline-${request.id}`}
        className="text-muted font-label mb-3 text-[10.5px] font-medium tracking-[0.08em] uppercase"
      >
        {t("identityProofing.sessions.timeline")}
      </h3>
      {events.isPending && (
        <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
      )}
      {events.isError && (
        <p className={ERROR}>{proofingErrorMessage(events.error, t)}</p>
      )}
      {events.isSuccess && events.data.length === 0 && (
        <p className="text-ink-soft text-[13px]">
          {t("identityProofing.sessions.noEvents")}
        </p>
      )}
      {events.isSuccess && events.data.length > 0 && (
        <ol className="relative flex flex-col gap-4">
          <span
            aria-hidden="true"
            className="bg-line absolute top-2 bottom-2 left-[13px] w-px"
          />
          {events.data.map((event) => {
            const visual = auditVisual(event.action);
            const detail = sessionEventDetail(event.metadata, t);
            return (
              <li key={event.id} className="relative flex gap-3">
                <span
                  className={`relative inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${AUDIT_TONE_CLASSES[visual.tone]}`}
                >
                  <Icon name={visual.icon} size={TIMELINE_ICON_SIZE} />
                </span>
                <div className="min-w-0 flex-1 pt-0.5">
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                    <span className="text-ink text-[13px] font-semibold">
                      {auditActionLabel(event.action, t)}
                    </span>
                    <time
                      dateTime={event.occurredAt}
                      className="text-muted text-[12px] whitespace-nowrap"
                    >
                      {formatWhen(event.occurredAt)}
                    </time>
                  </div>
                  <div className="text-ink-soft text-[12px]">
                    {[timelineActor(event, request, t, detail), ...detail]
                      .filter((part) => part !== null)
                      .join(" · ")}
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
