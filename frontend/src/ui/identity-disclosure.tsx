import { useEffect, useRef } from "react";
import * as yivi from "@privacybydesign/yivi-frontend";
import "@privacybydesign/yivi-css";
import * as React from "react";

const ELEMENT_ID = "yivi-disclosure-form";

interface Props {
  // Backend endpoint that starts the disclosure session and returns { token }.
  sessionUrl: string;
  // Called with the requestor token once the disclosure completes; the caller
  // exchanges it via the relevant accept endpoint.
  onToken: (token: string) => void;
  // Called when the session is cancelled or the QR expires.
  onAborted: () => void;
}

export function IdentityDisclosure({
  sessionUrl,
  onToken,
  onAborted,
}: Props): React.JSX.Element {
  const onTokenRef = useRef(onToken);
  const onAbortedRef = useRef(onAborted);

  useEffect(() => {
    onTokenRef.current = onToken;
    onAbortedRef.current = onAborted;
  });

  useEffect(() => {
    let cancelled = false;
    let token = "";

    const web = yivi.newWeb({
      debugging: import.meta.env.DEV,
      element: `#${ELEMENT_ID}`,
      minimal: true,
      session: {
        url: "",
        start: { url: () => sessionUrl, method: "POST" },
        mapping: {
          sessionToken: (r) => (token = (r as { token: string }).token),
        },
        result: false,
      },
    });

    web
      .start()
      .then(() => {
        if (!cancelled) onTokenRef.current(token);
      })
      .catch(() => {
        if (!cancelled) onAbortedRef.current();
      });

    return () => {
      cancelled = true;
      void web.abort();
    };
  }, [sessionUrl]);

  return <div id={ELEMENT_ID} />;
}
