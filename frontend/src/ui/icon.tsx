/**
 * Yivi iconography. Only the 34 bundled IrmaIcons glyphs are allowed — the
 * design system forbids mixing in Material / Heroicons / Lucide. SVGs are
 * inlined (not <img>) so `fill: currentColor` inherits the surrounding text
 * colour, matching the design system's recolouring rule.
 */

const DEFAULT_ICON_SIZE = 18;

const iconModules: Record<string, string> = import.meta.glob(
  "../assets/icons/*.svg",
  {
    eager: true,
    query: "?raw",
    import: "default",
  },
);

const iconMarkup: Record<string, string> = {};
for (const [path, markup] of Object.entries(iconModules)) {
  const name = path.split("/").pop()?.replace(".svg", "");
  if (name !== undefined) {
    iconMarkup[name] = markup;
  }
}

export type IconName =
  | "add"
  | "alert"
  | "arrow_back"
  | "arrow_front"
  | "birthdate"
  | "car"
  | "chevron_down"
  | "chevron_left"
  | "chevron_right"
  | "chevron_up"
  | "close"
  | "delete"
  | "edit"
  | "email"
  | "expand"
  | "favourite"
  | "filter"
  | "flag"
  | "hide"
  | "info"
  | "invalid"
  | "lock"
  | "logout"
  | "menu"
  | "personal"
  | "phone"
  | "question"
  | "scan_qrcode"
  | "search"
  | "settings"
  | "time"
  | "valid"
  | "view"
  | "warning";

interface IconProps {
  name: IconName;
  size?: number;
  className?: string;
}

export function Icon({
  name,
  size = DEFAULT_ICON_SIZE,
  className,
}: IconProps): React.JSX.Element | null {
  const markup = iconMarkup[name];
  if (markup === undefined) {
    return null;
  }
  return (
    <span
      className={`yivi-icon${className ? ` ${className}` : ""}`}
      style={{ width: size, height: size }}
      aria-hidden="true"
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
