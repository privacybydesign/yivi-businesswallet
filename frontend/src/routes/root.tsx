import { NavLink, Outlet } from "react-router";
import { useMeQuery, useLogoutMutation } from "../api/auth.queries";

export default function Root(): React.JSX.Element {
  const { data: me, isPending } = useMeQuery();
  const logout = useLogoutMutation();

  const loggedIn = !isPending && me != null;

  return (
    <>
      <header>
        <nav>
          <NavLink to="/">Home</NavLink>
          <NavLink to="/organizations">Organizations</NavLink>
          {loggedIn ? (
            <>
              <span>{me.email}</span>
              <button
                type="button"
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
              >
                Logout
              </button>
            </>
          ) : (
            <NavLink to="/login">Login</NavLink>
          )}
        </nav>
      </header>
      <main>
        <Outlet />
      </main>
      <footer></footer>
    </>
  );
}
