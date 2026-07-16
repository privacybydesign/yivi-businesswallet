const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;
const STEP = 1024;

// Formats a byte count as a human-readable size (e.g. "4.2 MB"). Uses one
// decimal place above KB, none for bytes.
export function formatBytes(bytes: number): string {
  if (bytes < STEP) {
    return `${bytes} B`;
  }
  let value = bytes;
  let unit = 0;
  while (value >= STEP && unit < UNITS.length - 1) {
    value /= STEP;
    unit++;
  }
  return `${value.toFixed(1)} ${UNITS[unit]}`;
}
