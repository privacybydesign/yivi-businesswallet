import { useCallback, useState } from "react";
import { ApiError } from "../api/http";
import type { SigningStart } from "../api/signing";
import type { CeremonyEnd } from "./signing-authorization-start";

// Signing a document as one action. A signature needs the signer's signing
// certificate, which they get by identifying themselves once; the document can
// only be prepared against that certificate, so the two are separate steps at
// the signing service. To the signer they are one: pressing Sign runs whichever
// steps are still needed, one after the other, in the same dialog.

export const SIGN_STEP = { link: "link", sign: "sign" } as const;
export type SignStep = (typeof SIGN_STEP)[keyof typeof SIGN_STEP];

// What the dialog shows: the started step, and where it is in the ceremony.
export interface SignCeremonyStep {
  step: SignStep;
  start: SigningStart;
  position: number;
  total: number;
}

const CONFLICT_STATUS = 409;
const NO_CREDENTIAL_CODE = "no_credential";

// The backend refusing a signature because the signer has no certificate (yet,
// or any more: an organisation that changed provider needs a new one).
export function isNoCredential(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    typeof error.body === "object" &&
    error.body !== null &&
    "code" in error.body &&
    error.body.code === NO_CREDENTIAL_CODE
  );
}

export interface SignCeremonyOptions<T> {
  // Whether the signer already has a certificate, as far as the page knows.
  linked: boolean;
  startLink: () => Promise<SigningStart>;
  startSign: (target: T) => Promise<SigningStart>;
  // The certificate was linked: what the page shows about it is stale.
  onLinked: () => void;
  // The signature ceremony ended; the request's own status says how.
  onSigned: (target: T, requestId: string | undefined) => void;
  // The certificate was not linked, so nothing was signed.
  onLinkFailed: () => void;
  // A step could not be started.
  onError: (error: Error) => void;
}

export interface SignCeremony<T> {
  // The step whose Yivi dialog is open, if any.
  current: SignCeremonyStep | null;
  // The target a step is being started for: its button shows a loader.
  starting: T | null;
  begin: (target: T) => void;
  // The open step ended: the ceremony goes on to the next, or is over.
  finished: (end: CeremonyEnd) => void;
  close: () => void;
}

const SINGLE_STEP = 1;
const BOTH_STEPS = 2;

function asError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}

export function useSignCeremony<T>(
  options: SignCeremonyOptions<T>,
): SignCeremony<T> {
  const [current, setCurrent] = useState<SignCeremonyStep | null>(null);
  const [target, setTarget] = useState<T | null>(null);
  const [starting, setStarting] = useState<T | null>(null);
  const {
    linked,
    startLink,
    startSign,
    onLinked,
    onSigned,
    onLinkFailed,
    onError,
  } = options;

  // A provider that is not the wallet's own shows its authorization on its own
  // page: the browser goes there and the ceremony picks up when it returns.
  const show = useCallback((step: SignCeremonyStep): void => {
    if (step.start.authorizeInApp === true) setCurrent(step);
    else window.location.assign(step.start.authorizeUrl);
  }, []);

  const link = useCallback(
    async (forTarget: T): Promise<void> => {
      setStarting(forTarget);
      try {
        const start = await startLink();
        show({
          step: SIGN_STEP.link,
          start,
          position: SINGLE_STEP,
          total: BOTH_STEPS,
        });
      } catch (error) {
        setCurrent(null);
        onError(asError(error));
      } finally {
        setStarting(null);
      }
    },
    [startLink, show, onError],
  );

  const sign = useCallback(
    async (forTarget: T, total: number): Promise<void> => {
      setStarting(forTarget);
      try {
        const start = await startSign(forTarget);
        show({ step: SIGN_STEP.sign, start, position: total, total });
      } catch (error) {
        // The page thought there was a certificate and the service knows none:
        // get one first, then sign. Only once, or a certificate that never
        // sticks would loop.
        if (total === SINGLE_STEP && isNoCredential(error)) {
          await link(forTarget);
          return;
        }
        setCurrent(null);
        onError(asError(error));
      } finally {
        setStarting(null);
      }
    },
    [startSign, show, link, onError],
  );

  const begin = useCallback(
    (forTarget: T): void => {
      setTarget(forTarget);
      void (linked ? sign(forTarget, SINGLE_STEP) : link(forTarget));
    },
    [linked, sign, link],
  );

  const finished = useCallback(
    (end: CeremonyEnd): void => {
      if (current === null || target === null) return;
      if (current.step === SIGN_STEP.sign) {
        setCurrent(null);
        onSigned(target, end.requestId);
        return;
      }
      if (end.linked !== true) {
        setCurrent(null);
        onLinkFailed();
        return;
      }
      onLinked();
      void sign(target, BOTH_STEPS);
    },
    [current, target, sign, onLinked, onSigned, onLinkFailed],
  );

  const close = useCallback((): void => setCurrent(null), []);

  return { current, starting, begin, finished, close };
}
