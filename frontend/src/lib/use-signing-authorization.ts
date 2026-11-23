import { useEffect, useRef } from "react";
import type { UseMutationResult } from "@tanstack/react-query";
import type { YiviCscAuthorization } from "../api/yivi-csc";
import {
  useCancelYiviCscAuthorizationMutation,
  useYiviCscAuthorizationQuery,
} from "../api/yivi-csc.queries";
import {
  authorizationAutoRedirect,
  authorizationView,
} from "./signing-authorization";
import type { AuthorizationView } from "./signing-authorization";
import { useSecondsLeft } from "./use-seconds-left";

export interface SigningAuthorizationState {
  authorization: YiviCscAuthorization | undefined;
  view: AuthorizationView;
  secondsLeft: number;
  cancel: UseMutationResult<YiviCscAuthorization, Error, void>;
}

function leaveFor(redirectUrl: string): void {
  window.location.assign(redirectUrl);
}

// One authorization at the built-in signing service, watched until the signer
// decides it in the Yivi app. Approved, or cancelled by the signer, it goes on
// to where the service says with nothing to read first: a URL on the API that
// carries the ceremony on. onDecided is handed that URL, once; by default the
// browser is sent there.
export function useSigningAuthorization(
  authorizationId: string,
  onDecided: (redirectUrl: string) => void = leaveFor,
): SigningAuthorizationState {
  const query = useYiviCscAuthorizationQuery(authorizationId);
  const cancel = useCancelYiviCscAuthorizationMutation(authorizationId);
  const authorization = query.data;
  const secondsLeft = useSecondsLeft(authorization?.expiresAt);
  const view = authorizationView({
    authorization,
    error: query.error,
    secondsLeft,
  });

  // The redirect ends the ceremony and can be followed only once, so an effect
  // that runs again for the same URL (a re-render, a development double run)
  // must not follow it a second time.
  const redirectUrl = authorizationAutoRedirect(authorization);
  const followed = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (redirectUrl === undefined || followed.current === redirectUrl) return;
    followed.current = redirectUrl;
    onDecided(redirectUrl);
  }, [redirectUrl, onDecided]);

  return { authorization, view, secondsLeft, cancel };
}
