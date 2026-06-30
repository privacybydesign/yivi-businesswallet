export type ToastTone = "success" | "error" | "info";

export interface Toast {
  id: number;
  tone: ToastTone;
  message: string;
}

const DEFAULT_DURATION_MS = 4000;

type Listener = (toasts: Toast[]) => void;

let toasts: Toast[] = [];
let nextId = 0;
const listeners = new Set<Listener>();

function emit(): void {
  for (const listener of listeners) {
    listener(toasts);
  }
}

function dismiss(id: number): void {
  toasts = toasts.filter((toast) => toast.id !== id);
  emit();
}

function show(tone: ToastTone, message: string): number {
  const id = nextId++;
  toasts = [...toasts, { id, tone, message }];
  emit();
  setTimeout(() => dismiss(id), DEFAULT_DURATION_MS);
  return id;
}

// Imperative so it can be called from anywhere — React components, query hooks,
// and the QueryClient's MutationCache (which lives outside the component tree).
export const toast = {
  success: (message: string): number => show("success", message),
  error: (message: string): number => show("error", message),
  info: (message: string): number => show("info", message),
  dismiss,
};

export function subscribeToasts(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getToasts(): Toast[] {
  return toasts;
}

// Lets a mutation opt out of the global error toast (e.g. forms that surface
// the error inline) via `useMutation({ meta: { suppressErrorToast: true } })`.
declare module "@tanstack/react-query" {
  interface Register {
    mutationMeta: {
      suppressErrorToast?: boolean;
    };
  }
}
