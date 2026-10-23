import { useEffect, useMemo } from "react";

// A picked file previews from a local object URL, freed when replaced.
export function useLogoPreview(file: File | null): string | null {
  const preview = useMemo(
    () => (file ? URL.createObjectURL(file) : null),
    [file],
  );
  useEffect(
    () => () => {
      if (preview) URL.revokeObjectURL(preview);
    },
    [preview],
  );
  return preview;
}
