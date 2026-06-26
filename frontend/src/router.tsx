import { createBrowserRouter } from "react-router";
import Root from "./routes/root";
import Home from "./routes/home";
import Organizations from "./routes/organizations";
import OrganizationDetail from "./routes/organization-detail";
import Login from "./routes/login";
import ProtectedRoute from "./routes/protected-route";

export const router = createBrowserRouter([
  { path: "/login", Component: Login },
  {
    path: "/",
    Component: Root,
    children: [
      {
        Component: ProtectedRoute,
        children: [
          { index: true, Component: Home },
          { path: "organizations", Component: Organizations },
          { path: ":orgSlug", Component: OrganizationDetail },
        ],
      },
    ],
  },
]);
