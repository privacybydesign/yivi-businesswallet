// The 19 VOG function-aspect codes an org may require (backend:
// internal/vog.FunctionAspects). Mirrored here so the settings panel can offer
// them as checkboxes; kept in sync with vog-codes.test.ts.
export const VOG_FUNCTION_ASPECTS = [
  "11",
  "12",
  "13",
  "21",
  "22",
  "36",
  "37",
  "38",
  "41",
  "43",
  "53",
  "61",
  "62",
  "63",
  "71",
  "84",
  "85",
  "86",
  "91",
] as const;

export function isFunctionAspect(code: string): boolean {
  return (VOG_FUNCTION_ASPECTS as readonly string[]).includes(code);
}
