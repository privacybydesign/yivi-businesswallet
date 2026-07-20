import { createContext, useContext } from "react";

// The active org's branding, shared with the shell chrome (sidebar, top bar) so
// header branding reads as one system. Both fields are undefined when no org is
// in view (admin/enroll screens), which falls back to the default Yivi identity.
export interface Brand {
  logoUri?: string;
  name?: string;
}

const BrandContext = createContext<Brand>({});

export const BrandProvider = BrandContext.Provider;

export function useBrand(): Brand {
  return useContext(BrandContext);
}
