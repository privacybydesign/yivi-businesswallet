import type { ReactNode } from "react";
import { FIELD_LABEL } from "../lib/attestation-form";

export function Field({
  id,
  label,
  required,
  error,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  error?: string;
  children: ReactNode;
}): React.JSX.Element {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className={FIELD_LABEL}>
        {label}
        {required && (
          <span aria-hidden className="text-error ml-0.5">
            *
          </span>
        )}
      </label>
      {children}
      {error && (
        <span
          id={`${id}-error`}
          role="alert"
          className="text-error text-[12px]"
        >
          {error}
        </span>
      )}
    </div>
  );
}
