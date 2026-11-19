// Whether a form's draft differs from the values it was opened with, field by
// field. A write-only secret counts as changed once anything is typed, so give
// its initial value as "".
export function draftChanged<T extends Record<string, unknown>>(
  draft: T,
  initial: T,
): boolean {
  return Object.keys(initial).some(
    (key) => !Object.is(draft[key], initial[key]),
  );
}
