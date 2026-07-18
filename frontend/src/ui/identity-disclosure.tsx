import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import * as React from "react";
import { getSessionStatus, startDisclosureSession } from "../api/auth";
import { Button } from "./button";
import { Icon } from "./icon";

const POLL_INTERVAL_MS = 1000;
// The hosted verifier reports only PENDING vs DONE — it cannot signal that a
// presentation has expired, so the wait is bounded client-side. After this long
// without a DONE the QR is treated as expired and a fresh one is offered rather
// than spinning indefinitely against a QR the wallet no longer accepts.
const SESSION_TIMEOUT_MS = 120_000;
const QR_SIZE = 240;
const UNIVERSAL_LINK_PREFIX = "https://open.yivi.app/-/openid4vp?";
const DONE_STATUS = "DONE";

// The disclosure lifecycle as far as this component can observe it: starting the
// session, waiting for the holder to complete it, or expired (bounded by our own
// timeout). A hard start/poll failure is reported to the caller via onAborted.
type DisclosurePhase = "starting" | "waiting" | "expired";

// universalLink rewrites the openid4vp:// deeplink into a Yivi universal link,
// which opens the wallet on this device and is scannable as a QR from another.
function universalLink(walletLink: string): string {
  const query = walletLink.split("?")[1] ?? "";
  return `${UNIVERSAL_LINK_PREFIX}${query}`;
}

interface Props {
  // Backend endpoint that starts the OpenID4VP session and returns { id, walletLink }.
  sessionUrl: string;
  // Called with the session id once the presentation completes; the caller
  // exchanges it via the relevant claim/accept endpoint.
  onToken: (id: string) => void;
  // Called when the session fails to start or a poll errors out — a technical
  // failure, distinct from an expiry (handled in-component with a refresh).
  onAborted: () => void;
}

export function IdentityDisclosure({
  sessionUrl,
  onToken,
  onAborted,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const [phase, setPhase] = useState<DisclosurePhase>("starting");
  const [qrDataUrl, setQrDataUrl] = useState("");
  const [walletUrl, setWalletUrl] = useState("");
  // Bumped by refresh() to restart the session (a new QR) without reloading.
  const [attempt, setAttempt] = useState(0);
  const onTokenRef = useRef(onToken);
  const onAbortedRef = useRef(onAborted);

  useEffect(() => {
    onTokenRef.current = onToken;
    onAbortedRef.current = onAborted;
  });

  useEffect(() => {
    let cancelled = false;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();

    const stop = (): void => {
      cancelled = true;
      if (pollTimer) clearTimeout(pollTimer);
      clearTimeout(expiryTimer);
      controller.abort();
    };

    const poll = async (id: string): Promise<void> => {
      let status: string;
      try {
        status = await getSessionStatus(id, controller.signal);
      } catch {
        if (!cancelled) {
          stop();
          onAbortedRef.current();
        }
        return;
      }
      if (cancelled) return;
      if (status === DONE_STATUS) {
        stop();
        onTokenRef.current(id);
        return;
      }
      pollTimer = setTimeout(() => void poll(id), POLL_INTERVAL_MS);
    };

    const run = async (): Promise<void> => {
      let session;
      try {
        session = await startDisclosureSession(sessionUrl, controller.signal);
      } catch {
        if (!cancelled) {
          stop();
          onAbortedRef.current();
        }
        return;
      }
      if (cancelled) return;

      const link = universalLink(session.walletLink);
      setWalletUrl(link);
      try {
        setQrDataUrl(
          await QRCode.toDataURL(link, { margin: 1, width: QR_SIZE }),
        );
      } catch {
        // The link button still works even if QR rendering fails.
      }
      setPhase("waiting");
      pollTimer = setTimeout(() => void poll(session.id), POLL_INTERVAL_MS);
    };

    const expiryTimer = setTimeout(() => {
      if (cancelled) return;
      stop();
      setPhase("expired");
    }, SESSION_TIMEOUT_MS);

    void run();

    return () => {
      stop();
    };
  }, [sessionUrl, attempt]);

  const refresh = (): void => {
    setQrDataUrl("");
    setWalletUrl("");
    setPhase("starting");
    setAttempt((a) => a + 1);
  };

  if (phase === "expired") {
    return (
      <div className="flex flex-col items-center gap-4">
        <div
          className="border-line-strong bg-surface rounded-yivi text-muted flex flex-col items-center justify-center gap-2 border px-4 text-center"
          style={{ width: QR_SIZE, height: QR_SIZE }}
        >
          <Icon name="time" size={32} />
          <span className="text-[13px]">{t("disclosure.expired")}</span>
        </div>
        <Button variant="secondary" icon="scan_qrcode" onClick={refresh}>
          {t("disclosure.refresh")}
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center gap-4">
      <div
        className="border-line-strong bg-surface rounded-yivi flex items-center justify-center border"
        style={{ width: QR_SIZE, height: QR_SIZE }}
      >
        {qrDataUrl ? (
          <img
            src={qrDataUrl}
            alt=""
            width={QR_SIZE}
            height={QR_SIZE}
            className="rounded-yivi"
          />
        ) : (
          <span
            aria-hidden="true"
            className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
          />
        )}
      </div>
      <p className="text-ink-soft text-center text-[13px]">
        {t("disclosure.scanHint")}
      </p>
      {walletUrl !== "" && (
        <a
          href={walletUrl}
          className="rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold"
        >
          {t("disclosure.openWallet")}
        </a>
      )}
    </div>
  );
}
