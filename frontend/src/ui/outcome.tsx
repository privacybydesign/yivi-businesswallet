import * as React from "react";
import { Icon } from "./icon";
import type { IconName } from "./icon";

type Tone = "success" | "info" | "error";

const TONE_CLASS: Record<Tone, string> = {
  success: "bg-success-bg text-success",
  info: "bg-highlight text-link",
  error: "bg-error-bg text-error",
};

interface Props {
  tone: Tone;
  icon: IconName;
  title: string;
  message: string;
  action?: React.ReactNode;
}

export function Outcome({
  tone,
  icon,
  title,
  message,
  action,
}: Props): React.JSX.Element {
  return (
    <div className="mt-6 flex flex-col items-center text-center">
      <span
        className={[
          "inline-flex h-12 w-12 items-center justify-center rounded-full",
          TONE_CLASS[tone],
        ].join(" ")}
      >
        <Icon name={icon} size={24} />
      </span>
      <h1 className="mt-4 text-[20px] font-bold">{title}</h1>
      <p className="text-ink-soft mt-1 text-[14px]">{message}</p>
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}
