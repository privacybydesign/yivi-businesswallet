import { Navigate, Outlet } from "react-router";
import { useMeQuery } from "../api/auth.queries";

/**
 * Gates the global platform-admin area. Backend endpoints already 403
 * non-admins; this keeps them out of the UI entirely.
 */
export default function AdminRoute(): React.JSX.Element | null {
  const { data: me, isPending } = useMeQuery();

  if (isPending) {
    return null;
  }
  if (me == null) {
    return <Navigate to="/login" replace />;
  }
  if (!me.isPlatformAdmin) {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}
