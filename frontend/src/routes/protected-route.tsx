import { Navigate, Outlet } from "react-router";
import { useMeQuery } from "../api/auth.queries";

export default function ProtectedRoute(): React.JSX.Element | null {
  const { data, isPending } = useMeQuery();

  if (isPending) {
    return null; // avoid a redirect flash before the session is known
  }

  // A failed refetch keeps the cached user: only a 401 (null) means signed out.
  if (data == null) {
    return <Navigate to="/login" replace />;
  }

  return <Outlet />;
}
