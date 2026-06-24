import { NavLink, Outlet } from "react-router";

export default function Root() {
  return (
    <>
      <header>
        <nav>
          <NavLink to="/">Home</NavLink>
          <NavLink to="/organizations">Organizations</NavLink>
          <NavLink to="/login">Login</NavLink>
        </nav>
      </header>
      <main>
        <Outlet />
      </main>
      <footer></footer>
    </>
  );
}
