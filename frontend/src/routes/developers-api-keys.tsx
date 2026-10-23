import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateApiKeyMutation,
  useProofingApiKeysQuery,
  useRevokeApiKeyMutation,
} from "../api/identity-proofing.queries";
import type { ProofingApiKey } from "../api/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Input, Modal, Table, Tag } from "../ui";
import { SecretReveal } from "./proofing-ui";

const ERROR = "text-error text-[12.5px]";
const HINT = "text-ink-soft text-[12.5px]";
const KEY_COLUMNS = 6;
const NEW_KEY_FORM = "proofing-new-api-key";
// The API reference the backend serves (internal/apidocs).
const API_DOCS_PATH = "/api/docs";

// The org's machine credentials: its software creates sessions and reads their
// outcome through the public proofing API with one. A key's secret is shown
// once, at creation.
export function ApiKeysTab({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const keys = useProofingApiKeysQuery(slug);
  const formatDate = useDateFormatter();
  const formatWhen = useWhenFormatter();
  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<ProofingApiKey | null>(null);
  const revoke = useRevokeApiKeyMutation(slug);

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className={HINT}>
          {t("identityProofing.apiKeys.intro")}{" "}
          <a
            href={absoluteApiUrl(API_DOCS_PATH)}
            target="_blank"
            rel="noreferrer"
            className="text-link font-semibold underline"
          >
            {t("identityProofing.overview.apiDocs")}
          </a>
        </p>
        <Button icon="add" onClick={() => setCreating(true)}>
          {t("identityProofing.apiKeys.create")}
        </Button>
      </div>
      <Card>
        <Table>
          <Table.Head>
            <Table.HeaderCell>
              {t("identityProofing.apiKeys.name")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.apiKeys.key")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.apiKeys.status")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.apiKeys.created")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.apiKeys.lastUsed")}
            </Table.HeaderCell>
            <Table.HeaderCell aria-hidden="true" />
          </Table.Head>
          <Table.Body>
            {keys.isPending && (
              <Table.State colSpan={KEY_COLUMNS}>
                {t("common.loading")}
              </Table.State>
            )}
            {keys.isError && (
              <Table.State colSpan={KEY_COLUMNS}>
                {proofingErrorMessage(keys.error, t)}
              </Table.State>
            )}
            {keys.isSuccess && keys.data.length === 0 && (
              <Table.State colSpan={KEY_COLUMNS}>
                {t("identityProofing.apiKeys.empty")}
              </Table.State>
            )}
            {keys.isSuccess &&
              keys.data.map((key) => (
                <Table.Row key={key.id}>
                  <Table.Cell className="font-semibold">{key.name}</Table.Cell>
                  <Table.Cell className="text-ink-soft font-mono text-[12.5px]">
                    {t("identityProofing.apiKeys.prefix", {
                      prefix: key.prefix,
                    })}
                  </Table.Cell>
                  <Table.Cell>
                    {key.revokedAt ? (
                      <Tag dot>{t("identityProofing.apiKeys.revoked")}</Tag>
                    ) : (
                      <Tag tone="green" dot>
                        {t("identityProofing.apiKeys.active")}
                      </Tag>
                    )}
                  </Table.Cell>
                  <Table.Cell>{formatDate(key.createdAt)}</Table.Cell>
                  <Table.Cell className="text-ink-soft">
                    {key.lastUsedAt
                      ? formatWhen(key.lastUsedAt)
                      : t("identityProofing.apiKeys.neverUsed")}
                  </Table.Cell>
                  <Table.Cell className="text-right">
                    {!key.revokedAt && (
                      <button
                        type="button"
                        className="text-error text-[12.5px] font-semibold hover:underline"
                        onClick={() => setRevoking(key)}
                      >
                        {t("identityProofing.apiKeys.revoke")}
                      </button>
                    )}
                  </Table.Cell>
                </Table.Row>
              ))}
          </Table.Body>
        </Table>
      </Card>
      {creating && (
        <NewKeyModal
          slug={slug}
          onClose={() => setCreating(false)}
          onCreated={(created) => {
            setCreating(false);
            setSecret(created);
          }}
        />
      )}
      {secret && (
        <SecretReveal
          title={t("identityProofing.apiKeys.createdTitle")}
          hint={t("identityProofing.apiKeys.createdHint")}
          secret={secret}
          onClose={() => setSecret(null)}
        />
      )}
      {revoking && (
        <ConfirmDialog
          title={t("identityProofing.apiKeys.revokeConfirm.title", {
            name: revoking.name,
          })}
          message={t("identityProofing.apiKeys.revokeConfirm.message")}
          confirmLabel={t("identityProofing.apiKeys.revoke")}
          busy={revoke.isPending}
          error={
            revoke.isError ? proofingErrorMessage(revoke.error, t) : undefined
          }
          onConfirm={() =>
            revoke.mutate(revoking.id, { onSuccess: () => setRevoking(null) })
          }
          onClose={() => {
            revoke.reset();
            setRevoking(null);
          }}
        />
      )}
    </>
  );
}

function NewKeyModal({
  slug,
  onClose,
  onCreated,
}: {
  slug: string;
  onClose: () => void;
  onCreated: (secret: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateApiKeyMutation(slug);
  const [name, setName] = useState("");
  const [touched, setTouched] = useState(false);
  const missing = name.trim() === "";

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (missing) {
      return;
    }
    create.mutate(
      { name: name.trim() },
      { onSuccess: (key) => onCreated(key.secret) },
    );
  }

  // The secret arrives once, in the answer to the POST, and only this modal
  // hands it on: closing it mid-request would drop the key's only copy.
  function close(): void {
    if (!create.isPending) {
      onClose();
    }
  }

  return (
    <Modal
      title={t("identityProofing.apiKeys.create")}
      closeLabel={t("common.close")}
      onClose={close}
      footer={
        <>
          <Button
            variant="secondary"
            size="sm"
            onClick={close}
            disabled={create.isPending}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="submit"
            form={NEW_KEY_FORM}
            size="sm"
            loading={create.isPending}
          >
            {t("identityProofing.apiKeys.create")}
          </Button>
        </>
      }
    >
      <form
        id={NEW_KEY_FORM}
        className="flex flex-col gap-1"
        onSubmit={submit}
        noValidate
      >
        <label
          htmlFor="proofing-api-key-name"
          className="text-ink-soft text-[12px] font-semibold"
        >
          {t("identityProofing.apiKeys.name")}
        </label>
        <Input
          id="proofing-api-key-name"
          value={name}
          placeholder={t("identityProofing.apiKeys.namePlaceholder")}
          aria-invalid={touched && missing}
          onChange={(e) => setName(e.target.value)}
        />
        {touched && missing && (
          <p className={ERROR}>{t("identityProofing.apiKeys.nameRequired")}</p>
        )}
        {create.isError && (
          <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
        )}
      </form>
    </Modal>
  );
}
