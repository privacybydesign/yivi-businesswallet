import { Outlet } from "react-router";
import { useMeQuery, useLogoutMutation } from "../api/auth.queries";
import { Sidebar } from "../ui";

export default function Root(): React.JSX.Element {
  const { data: me, isPending } = useMeQuery();
  const logout = useLogoutMutation();

  const email = !isPending && me != null ? me.email : null;

  return (
    <div className="flex min-h-screen">
      <Sidebar
        email={email}
        onLogout={() => logout.mutate()}
        loggingOut={logout.isPending}
      />
      <main className="min-w-0 flex-1">
        <Outlet />
      </main>
    </div>
  );
}
