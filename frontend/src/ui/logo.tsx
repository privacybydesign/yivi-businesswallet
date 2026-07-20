import logoUrl from "../assets/yivi-logo-horizontal.svg";

const LOGO_HEIGHT = 22;

// When an organization has set a custom logo it replaces the Yivi wordmark; the
// default is the product wordmark plus the "Business" tag.
export function Logo({
  src,
  alt,
}: {
  src?: string;
  alt?: string;
}): React.JSX.Element {
  if (src) {
    return (
      <img
        src={src}
        alt={alt ?? ""}
        style={{ maxHeight: LOGO_HEIGHT, maxWidth: "100%" }}
      />
    );
  }
  return (
    <div className="flex items-center gap-2">
      <img src={logoUrl} alt="Yivi" style={{ height: LOGO_HEIGHT }} />
      <span className="border-line text-muted ml-0.5 border-l pl-2 font-mono text-[10px] font-medium tracking-[0.12em] uppercase">
        Business
      </span>
    </div>
  );
}
