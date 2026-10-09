// What a stray dismissal of a modal does (a click on its overlay, Escape):
// close it, or nothing for a dialog that only its own controls may close.
export function modalDismissal({
  dismissible,
  onClose,
}: {
  dismissible: boolean;
  onClose: () => void;
}): (() => void) | undefined {
  return dismissible ? onClose : undefined;
}
