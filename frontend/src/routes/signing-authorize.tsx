import { useParams } from "react-router";
import * as React from "react";
import { useSigningAuthorization } from "../lib/use-signing-authorization";
import { Card, Logo } from "../ui";
import { SigningAuthorizationPanel } from "./signing-authorization-panel";

// The built-in signing service's authorization page. A signing ceremony sends
// the signer's browser here to approve one thing in the Yivi app: identifying
// themselves for their signing certificate, or one signature. The signer has no
// session here, a member or not: the authorization id in the address is the
// only key. Once decided, the service names where the browser goes next, a full
// URL on the API that carries the ceremony on.
//
// The wallet's own signing pages do not send the browser here: they show the
// same step in a dialog (signing-authorization-dialog). This page is what a
// browser that was redirected by oauth2/authorize lands on.
export default function SigningAuthorize(): React.JSX.Element {
  const { id } = useParams();
  // Guaranteed by the ":id" route segment this component mounts under.
  const state = useSigningAuthorization(id!);

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-6 sm:p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        <SigningAuthorizationPanel state={state} />
      </Card>
    </div>
  );
}
