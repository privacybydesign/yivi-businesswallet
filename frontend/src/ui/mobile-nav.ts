import { createContext, useContext } from "react";

// Lets the shared TopBar open the off-canvas navigation drawer without every
// page having to thread a handler down. Root provides the value; TopBar reads
// it via useMobileNav(). Kept in a component-free module so React Fast Refresh
// stays happy.
interface MobileNavValue {
  openNav: () => void;
}

export const MobileNavContext = createContext<MobileNavValue | null>(null);

// Returns null outside a provider so TopBar can render standalone (e.g. tests)
// without a menu button.
export function useMobileNav(): MobileNavValue | null {
  return useContext(MobileNavContext);
}
