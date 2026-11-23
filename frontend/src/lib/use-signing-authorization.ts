import { useEffect } from "react";
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

// One authorization at the built-in signing service, watched until the signer
// decides it in the Yivi app. Approved, or cancelled by the signer, the browser
// goes on to where the service says with nothing to read first: a URL on the
// API that carries the ceremony on and returns to the page that started it.
export function useSigningAuthorization(
  authorizationId: string,
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

  const redirectUrl = authorizationAutoRedirect(authorization);
  useEffect(() => {
    if (redirectUrl !== undefined) window.location.assign(redirectUrl);
  }, [redirectUrl]);

  return { authorization, view, secondsLeft, cancel };
}
