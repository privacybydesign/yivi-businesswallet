import * as React from "react";

type AvatarTone = "blue" | "rose" | "green" | "amber" | "violet" | "slate";
type AvatarSize = "md" | "lg" | "xl";
type AvatarFit = "contain" | "cover";

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
  xl: "w-20 h-20 text-[26px]",
};

const FIT_CLASSES: Record<AvatarFit, string> = {
  contain: "object-contain",
  cover: "object-cover",
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
// pre-computed `initials` (e.g. a person's preferred + last initial). An optional
// `src` renders that image (an org's uploaded logo, a person's avatar photo) in
// place of the initials, keeping the circular frame; `alt` labels it (initials
// stay decorative). When `src` is empty the initials fallback is used. `fit`
// picks how the image fills the frame: a logo is shown whole (`contain`, the
// default), a portrait photo fills the circle (`cover`).
type AvatarProps = {
  tone?: AvatarTone;
  size?: AvatarSize;
  src?: string;
  alt?: string;
  fit?: AvatarFit;
} & ({ name: string; initials?: string } | { name?: string; initials: string });

export function Avatar({
  name,
  initials,
  tone,
  size = "md",
  src,
  alt,
  fit = "contain",
}: AvatarProps): React.JSX.Element {
  // A src the browser cannot load falls back to the initials rather than leaving
  // a broken image: an avatar URL carries the photo's version, so one held in a
  // rendered list can outlive the photo it points at.
  const [failed, setFailed] = React.useState<string | null>(null);
  const showImage = src !== undefined && src !== "" && src !== failed;

  if (showImage) {
    return (
      <img
        src={src}
        alt={alt ?? ""}
        onError={() => setFailed(src)}
        className={[
          "bg-surface-3 shrink-0 rounded-full",
          FIT_CLASSES[fit],
          SIZE_CLASSES[size],
        ].join(" ")}
      />
    );
  }
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
