import { useTranslation } from "react-i18next";
import type { AttestationSchema } from "../api/attestations";
import {
  useAttestationRulebooksQuery,
  useImportAttestationRulebookMutation,
} from "../api/attestations.queries";
import { Button, Card, Modal, Tag } from "../ui";

// Lists the WE BUILD rulebooks and imports one as a schema. A rulebook the
// organization already holds (same vct) cannot be imported twice.
export function AttestationRulebookImport({
  slug,
  schemas,
  onClose,
}: Readonly<{
  slug: string;
  schemas: AttestationSchema[];
  onClose: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const rulebooks = useAttestationRulebooksQuery(slug);
  const importRulebook = useImportAttestationRulebookMutation(slug);
  const held = new Set(schemas.map((schema) => schema.vct));

  return (
    <Modal
      title={t("attestations.rulebooks.title")}
      closeLabel={t("common.close")}
      onClose={onClose}
      wide
    >
      <div className="flex flex-col gap-3">
        <p className="text-ink-soft text-[14px]">
          {t("attestations.rulebooks.intro")}
        </p>
        {rulebooks.error && (
          <p className="text-error text-[14px]">
            {t("attestations.loadError", { message: rulebooks.error.message })}
          </p>
        )}
        {importRulebook.error && (
          <p className="text-error text-[14px]">
            {t("attestations.rulebooks.importError", {
              message: importRulebook.error.message,
            })}
          </p>
        )}
        {rulebooks.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : (
          (rulebooks.data ?? []).map((rulebook) => {
            const imported = held.has(rulebook.vct);
            return (
              <Card
                key={rulebook.id}
                className="flex items-start justify-between gap-3 p-4"
              >
                <div className="min-w-0">
                  <div className="text-ink font-semibold">{rulebook.name}</div>
                  <div className="text-ink-soft truncate font-mono text-[12px]">
                    {rulebook.vct}
                  </div>
                  <div className="text-ink-soft mt-1 text-[12.5px]">
                    {rulebook.subjectType === "organization"
                      ? t("attestations.rulebooks.aboutOrganization")
                      : t("attestations.rulebooks.aboutPerson")}
                    {" · "}
                    {t("attestations.rulebooks.attributeCount", {
                      count: rulebook.attributes.length,
                    })}
                  </div>
                </div>
                {imported ? (
                  <Tag tone="green">{t("attestations.rulebooks.imported")}</Tag>
                ) : (
                  <Button
                    size="sm"
                    icon="add"
                    loading={
                      importRulebook.isPending &&
                      importRulebook.variables === rulebook.id
                    }
                    disabled={importRulebook.isPending}
                    onClick={() => importRulebook.mutate(rulebook.id)}
                  >
                    {t("attestations.rulebooks.import")}
                  </Button>
                )}
              </Card>
            );
          })
        )}
      </div>
    </Modal>
  );
}
