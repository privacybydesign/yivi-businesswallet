// The path and query of a URL the signing ceremony hands out, for the API client
// to request. Those URLs are absolute (they are what a browser is sent to), while
// every API call here is a path under the API's base, so the origin is dropped:
// the request then goes to the API this app is configured for and nowhere else,
// whatever the URL said.
export function authorizePath(authorizeUrl: string): string {
  const url = new URL(authorizeUrl);
  return `${url.pathname}${url.search}`;
}

// The ceremony's own callback, the only redirect a page finishes itself.
const CALLBACK_SUFFIX = "/signing/callback";

// The path and query of a decided authorization's redirect, when it is the
// ceremony's callback. Anything else is somewhere the browser has to go.
export function callbackPath(redirectUrl: string): string | undefined {
  const url = new URL(redirectUrl);
  return url.pathname.endsWith(CALLBACK_SUFFIX)
    ? `${url.pathname}${url.search}`
    : undefined;
}

// How a ceremony ended, read from where its callback would have sent the
// browser: the page that started it, with the outcome in the query.
export interface CeremonyEnd {
  // A link ceremony: whether the signing certificate was linked.
  linked: boolean | undefined;
  // A signature: the request it was for. Its status says how it went.
  requestId: string | undefined;
}

const LINK_PARAM = "link";
const LINK_OK = "ok";
const REQUEST_PARAM = "request";

export function ceremonyEnd(destination: string): CeremonyEnd {
  const params = new URL(destination).searchParams;
  const link = params.get(LINK_PARAM);
  return {
    linked: link === null ? undefined : link === LINK_OK,
    requestId: params.get(REQUEST_PARAM) ?? undefined,
  };
}
