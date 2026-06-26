type AvatarTone = "blue" | "rose" | "green" | "amber" | "violet" | "slate";
type AvatarSize = "md" | "lg";

const TONE_CLASSES: Record<AvatarTone, string> = {
  blue: "bg-highlight text-link",
  rose: "bg-[#F5DDE4] text-[#9A2744]",
  green: "bg-success-bg text-success",
  amber: "bg-warning-bg text-warning-fg",
  violet: "bg-[#ECE3F4] text-[#5B3B85]",
  slate: "bg-[#E4E2DF] text-ink",
};

const SIZE_CLASSES: Record<AvatarSize, string> = {
  md: "w-7 h-7 text-[11.5px]",
  lg: "w-12 h-12 text-[17px]",
};

const MAX_INITIALS = 2;

function initialsFrom(name: string): string {
  return name
    .split(" ")
    .map((word) => word[0] ?? "")
    .slice(0, MAX_INITIALS)
    .join("")
    .toUpperCase();
}

interface AvatarProps {
  name: string;
  tone?: AvatarTone;
  size?: AvatarSize;
}

export function Avatar({
  name,
  tone = "blue",
  size = "md",
}: AvatarProps): React.JSX.Element {
  return (
    <span
      className={[
        "font-display inline-flex shrink-0 items-center justify-center rounded-full font-semibold",
        TONE_CLASSES[tone],
        SIZE_CLASSES[size],
      ].join(" ")}
      aria-hidden="true"
    >
      {initialsFrom(name)}
    </span>
  );
}
