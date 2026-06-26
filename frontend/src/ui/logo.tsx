import logoUrl from "../assets/yivi-logo-horizontal.svg";

const LOGO_HEIGHT = 22;

export function Logo(): React.JSX.Element {
  return (
    <div className="flex items-center gap-2">
      <img src={logoUrl} alt="Yivi" style={{ height: LOGO_HEIGHT }} />
      <span className="border-line text-muted ml-0.5 border-l pl-2 font-mono text-[10px] font-medium tracking-[0.12em] uppercase">
        Business
      </span>
    </div>
  );
}
