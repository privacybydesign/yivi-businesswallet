import { useEffect, useState } from "react";
import * as React from "react";
import type { HostedProofing } from "../api/identity-proofing";
import {
  HOSTED_PREVIEW_READY,
  isHostedPreviewMessage,
} from "../lib/hosted-preview";
import { HostedLayout, HostedPage } from "./proof";

// The hosted page as the branding settings show it, in their frame: the page
// they send, never one of a real link, and nothing on it can be pressed.
export default function HostedPreview(): React.JSX.Element | null {
  const [page, setPage] = useState<HostedProofing>();

  useEffect(() => {
    const origin = window.location.origin;
    function receive(event: MessageEvent): void {
      if (
        event.origin === origin &&
        event.source === window.parent &&
        isHostedPreviewMessage(event.data)
      ) {
        setPage(event.data.page);
      }
    }
    window.addEventListener("message", receive);
    window.parent.postMessage({ type: HOSTED_PREVIEW_READY }, origin);
    return () => window.removeEventListener("message", receive);
  }, []);

  if (page === undefined) {
    return null;
  }
  return (
    <div inert>
      <HostedLayout>
        {/* A new page per change, so its steps start over on the overview. */}
        <HostedPage key={JSON.stringify(page)} token="" page={page} />
      </HostedLayout>
    </div>
  );
}
