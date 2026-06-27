import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useCreateDepartmentMutation,
  useDeleteDepartmentMutation,
  useOrganizationDepartmentsQuery,
  useUpdateDepartmentMutation,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const CONFLICT_STATUS = 409;

function errorCode(error: ApiError): string | null {
  const body = error.body;
  if (typeof body === "object" && body !== null && "code" in body) {
    const code = (body as { code?: unknown }).code;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function departmentError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    const code = errorCode(error);
    if (code === "name_taken") {
      return t("departments.nameTaken");
    }
    if (code === "department_in_use") {
      return t("departments.inUse");
    }
  }
  return t("departments.error", { message: error.message });
}

export function DepartmentSettings({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const departments = useOrganizationDepartmentsQuery(slug);
  const create = useCreateDepartmentMutation(slug);
  const update = useUpdateDepartmentMutation(slug);
  const remove = useDeleteDepartmentMutation(slug);

  const [name, setName] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");

  function handleCreate(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    const trimmed = name.trim();
    if (trimmed === "") {
      return;
    }
    create.mutate({ name: trimmed }, { onSuccess: () => setName("") });
  }

  function startEdit(id: string, current: string): void {
    update.reset();
    setEditingId(id);
    setDraft(current);
  }

  function handleRename(id: string): void {
    const trimmed = draft.trim();
    if (trimmed === "") {
      return;
    }
    update.mutate(
      { departmentId: id, name: trimmed },
      { onSuccess: () => setEditingId(null) },
    );
  }

  const actionError = update.isError
    ? update.error
    : remove.isError
      ? remove.error
      : null;

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">{t("departments.heading")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("departments.description")}
      </p>

      <form onSubmit={handleCreate} className="mt-4 flex gap-2">
        <div className="flex-1">
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("departments.namePlaceholder")}
          />
        </div>
        <Button
          type="submit"
          icon="add"
          disabled={name.trim() === "" || create.isPending}
        >
          {create.isPending ? t("departments.adding") : t("departments.add")}
        </Button>
      </form>
      {create.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {departmentError(create.error, t)}
        </p>
      )}

      <div className="mt-5">
        {departments.isError ? (
          <p className="text-error text-[13px]">
            {t("departments.loadError", {
              message: departments.error.message,
            })}
          </p>
        ) : departments.isPending ? (
          <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
        ) : departments.data.length === 0 ? (
          <p className="text-ink-soft text-[13px]">{t("departments.empty")}</p>
        ) : (
          <ul className="rounded-yivi border-line divide-line divide-y border">
            {departments.data.map((department) => (
              <li
                key={department.id}
                className="flex items-center gap-2 px-4 py-2.5"
              >
                {editingId === department.id ? (
                  <>
                    <div className="flex-1">
                      <Input
                        value={draft}
                        onChange={(event) => setDraft(event.target.value)}
                        autoFocus
                      />
                    </div>
                    <Button
                      size="sm"
                      onClick={() => handleRename(department.id)}
                      disabled={draft.trim() === "" || update.isPending}
                    >
                      {t("departments.save")}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setEditingId(null)}
                    >
                      {t("departments.cancel")}
                    </Button>
                  </>
                ) : (
                  <>
                    <span className="text-ink flex-1 text-[13.5px]">
                      {department.name}
                    </span>
                    <Button
                      size="sm"
                      variant="ghost"
                      icon="edit"
                      onClick={() => startEdit(department.id, department.name)}
                    >
                      {t("departments.rename")}
                    </Button>
                    <Button
                      size="sm"
                      variant="danger"
                      icon="delete"
                      onClick={() =>
                        remove.mutate({ departmentId: department.id })
                      }
                      disabled={remove.isPending}
                    >
                      {t("departments.delete")}
                    </Button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
      {actionError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {departmentError(actionError, t)}
        </p>
      )}
    </Card>
  );
}
