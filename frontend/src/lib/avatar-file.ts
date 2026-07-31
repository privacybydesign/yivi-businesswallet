// Client-side checks on a chosen avatar photo, so an obviously unusable file is
// refused before it is uploaded. Kept in step with the backend, which validates
// the same things authoritatively: user.MaxAvatarUploadBytes for the size, and the
// formats NormalizeAvatar can decode for the types (no SVG — an avatar is a
// photo).
export const ACCEPTED_AVATAR_TYPES = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
] as const;

export const MAX_AVATAR_BYTES = 8 * 1024 * 1024;

// What is wrong with a chosen file, or null when it is acceptable. The caller maps
// this to the message it shows.
export type AvatarFileProblem = "type" | "size" | "empty";

export function avatarFileProblem(file: {
  type: string;
  size: number;
}): AvatarFileProblem | null {
  if (!ACCEPTED_AVATAR_TYPES.includes(file.type as never)) {
    return "type";
  }
  if (file.size === 0) {
    return "empty";
  }
  if (file.size > MAX_AVATAR_BYTES) {
    return "size";
  }
  return null;
}
