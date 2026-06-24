import { createBrowserRouter } from "react-router";
import Root from "./routes/root";
import Home from "./routes/home";
import Health from "./routes/health";
import Organizations from "./routes/organizations";

export const router = createBrowserRouter([
  {
    path: "/",
    Component: Root,
    children: [
      { index: true, Component: Home },
      { path: "health", Component: Health },
      { path: "organizations", Component: Organizations },
    ],
  },
]);
