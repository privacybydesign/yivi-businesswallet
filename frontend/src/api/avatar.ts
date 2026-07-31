import { z } from "zod";
import { absoluteApiUrl, request } from "./http";

// The path the changed photo is served from, or "" once it has been removed.
const avatarResultSchema = z.object({ avatarUri: z.string() });

export type AvatarResult = z.infer<typeof avatarResultSchema>;

// MAX_AVATAR_BYTES mirrors useravatar.MaxAvatarBytes. The browser downscales
// every picked file to AVATAR_TARGET_DIMENSION and re-encodes it as JPEG before
// uploading, which is well inside both this cap and the backend's dimension
// limit — so the caps are the guard against a hand-made request, not the path a
// person takes.
export const MAX_AVATAR_BYTES = 512 * 1024;
export const AVATAR_TARGET_DIMENSION = 512;

const AVATAR_FORM_FIELD = "photo";
// The server sniffs the bytes and ignores the name, but a multipart file part
// needs one; the browser would otherwise send "blob".
const AVATAR_FILE_NAME = "avatar";

// The backend returns an avatar as a path on the API; make it absolute so an
// <img> loads it from the API origin even when the SPA is served elsewhere
// (mirrors withAbsoluteLogo in theme.ts). Every response that carries an
// avatarUri — /me, a member, an audit actor — goes through this.
export function withAbsoluteAvatar<T extends { avatarUri: string }>(
  subject: T,
): T {
  return subject.avatarUri
    ? { ...subject, avatarUri: absoluteApiUrl(subject.avatarUri) }
    : subject;
}

export async function uploadMyAvatar(
  photo: Blob,
  signal?: AbortSignal,
): Promise<AvatarResult> {
  const form = new FormData();
  form.append(AVATAR_FORM_FIELD, photo, AVATAR_FILE_NAME);
  return withAbsoluteAvatar(
    await request("/api/v1/me/avatar", {
      schema: avatarResultSchema,
      method: "PUT",
      body: form,
      signal,
    }),
  );
}

export function removeMyAvatar(signal?: AbortSignal): Promise<AvatarResult> {
  return request("/api/v1/me/avatar", {
    schema: avatarResultSchema,
    method: "DELETE",
    signal,
  });
}
