// The path and query of a ceremony's authorize URL, for the API client to
// request. The URL the backend hands out is absolute (it is what a browser is
// sent to), while every API call here is a path under the API's base, so the
// origin is dropped: the request then goes to the API this app is configured
// for and nowhere else, whatever the URL said.
export function authorizePath(authorizeUrl: string): string {
  const url = new URL(authorizeUrl);
  return `${url.pathname}${url.search}`;
}
