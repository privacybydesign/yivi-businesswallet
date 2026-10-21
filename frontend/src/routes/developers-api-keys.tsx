import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateApiKeyMutation,
  useProofingApiKeysQuery,
  useRevokeApiKeyMutation,
} from "../api/identity-proofing.queries";
import type {
  CreatedProofingApiKey,
  ProofingApiKey,
} from "../api/identity-proofing";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Input, Modal, Table } from "../ui";
import { SecretReveal } from "./proofing-ui";

const ERROR = "text-error text-[12.5px]";
const HINT = "text-ink-soft text-[12.5px]";
const KEY_COLUMNS = 4;
const NEW_KEY_FORM = "proofing-new-api-key";
const NEW_KEY_HINT = "proofing-api-key-name-hint";

// The org's machine credentials: its software creates sessions and reads their
// outcome through the public proofing API with one. A token works on every flow
// of the org, and its secret is shown once, at creation. A revoked token is
// not listed: it no longer does anything, and the audit log keeps the record.
export function ApiTokensSection({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const keys = useProofingApiKeysQuery(slug);
  const formatDate = useDateFormatter();
  const formatWhen = useWhenFormatter();
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedProofingApiKey | null>(null);
  const [revoking, setRevoking] = useState<ProofingApiKey | null>(null);
  const revoke = useRevokeApiKeyMutation(slug);
  const active = keys.data?.filter((key) => !key.revokedAt);

  return (
    <>
      <Card>
        <div className="border-line flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
          <div>
            <h2 className="font-display text-[17px] font-bold">
              {t("identityProofing.apiKeys.title")}
            </h2>
            <p className={`${HINT} mt-1`}>
              {t("identityProofing.apiKeys.intro")}
            </p>
          </div>
          <Button icon="add" onClick={() => setCreating(true)}>
            {t("identityProofing.apiKeys.create")}
          </Button>
        </div>
        <Table>
          <Table.Head>
            <Table.HeaderCell scope="col">
              {t("identityProofing.apiKeys.name")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofing.apiKeys.created")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofing.apiKeys.lastUsed")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col" srOnly>
              {t("identityProofing.apiKeys.actions")}
            </Table.HeaderCell>
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
            {keys.isSuccess && !active?.length && (
              <Table.State colSpan={KEY_COLUMNS}>
                {t("identityProofing.apiKeys.empty")}
              </Table.State>
            )}
            {keys.isSuccess &&
              active?.map((key) => (
                <Table.Row key={key.id}>
                  <Table.Cell className="font-semibold">{key.name}</Table.Cell>
                  <Table.Cell>
                    {formatDate(key.createdAt)}
                    {key.createdByName && ` · ${key.createdByName}`}
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft">
                    {key.lastUsedAt
                      ? formatWhen(key.lastUsedAt)
                      : t("identityProofing.apiKeys.neverUsed")}
                  </Table.Cell>
                  <Table.Cell className="text-right">
                    <button
                      type="button"
                      className="text-error text-[12.5px] font-semibold hover:underline"
                      aria-label={t("identityProofing.apiKeys.revokeNamed", {
                        name: key.name,
                      })}
                      onClick={() => setRevoking(key)}
                    >
                      {t("identityProofing.apiKeys.revoke")}
                    </button>
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
          onCreated={(key) => {
            setCreating(false);
            setCreated(key);
          }}
        />
      )}
      {created && (
        <SecretReveal
          title={t("identityProofing.apiKeys.createdTitle")}
          hint={t("identityProofing.apiKeys.createdHint", {
            name: created.name,
          })}
          footnote={t("identityProofing.apiKeys.createdNote")}
          secret={created.secret}
          onClose={() => setCreated(null)}
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
  onCreated: (key: CreatedProofingApiKey) => void;
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
    create.mutate({ name: name.trim() }, { onSuccess: onCreated });
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
          aria-invalid={touched && missing}
          aria-describedby={NEW_KEY_HINT}
          onChange={(e) => setName(e.target.value)}
        />
        <p id={NEW_KEY_HINT} className={HINT}>
          {t("identityProofing.apiKeys.nameHint")}
        </p>
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
