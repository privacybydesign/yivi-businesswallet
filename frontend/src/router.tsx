import { createBrowserRouter } from "react-router";
import Root from "./routes/root";
import RootRedirect from "./routes/root-redirect";
import ProtectedRoute from "./routes/protected-route";
import AdminRoute from "./routes/admin-route";
import Login from "./routes/login";
import Dashboard from "./routes/dashboard";
import Members from "./routes/members";
import AdminDashboard from "./routes/admin-dashboard";
import AllOrganizations from "./routes/all-organizations";
import CreateOrganization from "./routes/create-organization";

export const router = createBrowserRouter([
  { path: "/login", Component: Login },
  {
    Component: ProtectedRoute,
    children: [
      { index: true, Component: RootRedirect },
      {
        Component: Root,
        children: [
          {
            path: ":orgSlug",
            children: [
              { index: true, Component: Dashboard },
              { path: "members", Component: Members },
            ],
          },
          {
            Component: AdminRoute,
            children: [
              {
                path: "admin",
                children: [
                  { index: true, Component: AdminDashboard },
                  {
                    path: "organizations",
                    children: [
                      { index: true, Component: AllOrganizations },
                      { path: "new", Component: CreateOrganization },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  },
]);
