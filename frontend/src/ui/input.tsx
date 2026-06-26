import type { InputHTMLAttributes } from "react";
import type { IconName } from "./icon";
import { Icon } from "./icon";

const ICON_SIZE = 16;

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: IconName;
}

export function Input({
  icon,
  className,
  ...rest
}: InputProps): React.JSX.Element {
  const field = (
    <input
      className={[
        "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border",
        "text-[13.5px] transition-colors outline-none",
        "placeholder:text-muted",
        "focus:border-ink focus:ring-ink/10 focus:ring-3",
        icon ? "pr-3 pl-9" : "px-3",
        className ?? "",
      ].join(" ")}
      {...rest}
    />
  );

  if (!icon) {
    return field;
  }

  return (
    <div className="relative inline-flex w-full items-center">
      <Icon
        name={icon}
        size={ICON_SIZE}
        className="text-muted absolute left-3"
      />
      {field}
    </div>
  );
}
