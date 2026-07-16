import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import * as React from "react";
import { getSessionStatus, startDisclosureSession } from "../api/auth";

const POLL_INTERVAL_MS = 1000;
const QR_SIZE = 240;
const UNIVERSAL_LINK_PREFIX = "https://open.yivi.app/-/openid4vp?";
const DONE_STATUS = "DONE";

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
  // Called when the session fails to start or the presentation errors out.
  onAborted: () => void;
}

export function IdentityDisclosure({
  sessionUrl,
  onToken,
  onAborted,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const [qrDataUrl, setQrDataUrl] = useState("");
  const [walletUrl, setWalletUrl] = useState("");
  const onTokenRef = useRef(onToken);
  const onAbortedRef = useRef(onAborted);

  useEffect(() => {
    onTokenRef.current = onToken;
    onAbortedRef.current = onAborted;
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();

    const poll = async (id: string): Promise<void> => {
      let status: string;
      try {
        status = await getSessionStatus(id, controller.signal);
      } catch {
        if (!cancelled) onAbortedRef.current();
        return;
      }
      if (cancelled) return;
      if (status === DONE_STATUS) {
        onTokenRef.current(id);
        return;
      }
      timer = setTimeout(() => void poll(id), POLL_INTERVAL_MS);
    };

    const run = async (): Promise<void> => {
      let session;
      try {
        session = await startDisclosureSession(sessionUrl, controller.signal);
      } catch {
        if (!cancelled) onAbortedRef.current();
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
      timer = setTimeout(() => void poll(session.id), POLL_INTERVAL_MS);
    };

    void run();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      controller.abort();
    };
  }, [sessionUrl]);

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
