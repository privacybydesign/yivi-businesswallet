import { useEffect, useState } from "react";
import { secondsUntil } from "./identity-proofing";

const COUNTDOWN_TICK_MS = 1000;

// Seconds left until expiresAt, re-read every second; 0 without one.
export function useSecondsLeft(expiresAt: string | undefined): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (expiresAt === undefined) return;
    const timer = setInterval(() => setNow(Date.now()), COUNTDOWN_TICK_MS);
    return () => clearInterval(timer);
  }, [expiresAt]);
  return expiresAt === undefined ? 0 : secondsUntil(expiresAt, now);
}
