import { Navigate, Outlet } from "react-router";
import { useMeQuery } from "../api/auth.queries";

export default function ProtectedRoute(): React.JSX.Element | null {
  const { data, isPending, isError } = useMeQuery();

  if (isPending) {
    return null; // avoid a redirect flash before the session is known
  }

  if (isError || data === null) {
    return <Navigate to="/login" replace />;
  }

  return <Outlet />;
}
