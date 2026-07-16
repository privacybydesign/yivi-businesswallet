import { cloneElement, isValidElement } from "react";
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
  const errorId = `${id}-error`;
  // Link the control to its error message so a screen reader announces it when
  // the field is focused again later (role="alert" only fires on first render).
  const control =
    error && isValidElement<{ "aria-describedby"?: string }>(children)
      ? cloneElement(children, { "aria-describedby": errorId })
      : children;
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
      {control}
      {error && (
        <span id={errorId} role="alert" className="text-error text-[12px]">
          {error}
        </span>
      )}
    </div>
  );
}
