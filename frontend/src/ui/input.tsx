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
        "h-9 w-full rounded-yivi border border-line-strong bg-surface text-ink",
        "text-[13.5px] outline-none transition-colors",
        "placeholder:text-muted",
        "focus:border-ink focus:ring-3 focus:ring-ink/10",
        icon ? "pl-9 pr-3" : "px-3",
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
        className="absolute left-3 text-muted"
      />
      {field}
    </div>
  );
}
