const SECOND_MS = 1_000;
const MINUTE_MS = 60 * SECOND_MS;

export const DEFAULT_STALE_TIME_MS = 30 * SECOND_MS;

// For reads that change only when someone saves them (my orgs, theme,
// signing availability). Every save of one must invalidate its key.
export const RARELY_CHANGING_STALE_TIME_MS = 5 * MINUTE_MS;
