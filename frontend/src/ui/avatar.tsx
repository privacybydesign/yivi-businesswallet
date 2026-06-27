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

const TONES = ["blue", "rose", "green", "amber", "violet", "slate"] as const;

const TONE_HASH_MULTIPLIER = 31;

function toneFromName(name: string): AvatarTone {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = (hash * TONE_HASH_MULTIPLIER + name.charCodeAt(i)) | 0;
  }
  return TONES[Math.abs(hash) % TONES.length] ?? "blue";
}

const MAX_INITIALS = 2;

function initialsFrom(name: string): string {
  return name
    .split(" ")
    .map((word) => word[0] ?? "")
    .slice(0, MAX_INITIALS)
    .join("")
    .toUpperCase();
}

// Either give a `name` (initials derived from its words, e.g. an org) or
// pre-computed `initials` (e.g. a person's preferred + last initial).
type AvatarProps = {
  tone?: AvatarTone;
  size?: AvatarSize;
} & ({ name: string; initials?: string } | { name?: string; initials: string });

export function Avatar({
  name,
  initials,
  tone,
  size = "md",
}: AvatarProps): React.JSX.Element {
  const text = initials ?? initialsFrom(name ?? "");
  const resolvedTone = tone ?? toneFromName(text);
  return (
    <span
      className={[
        "font-display inline-flex shrink-0 items-center justify-center rounded-full font-semibold",
        TONE_CLASSES[resolvedTone],
        SIZE_CLASSES[size],
      ].join(" ")}
      aria-hidden="true"
    >
      {text}
    </span>
  );
}
