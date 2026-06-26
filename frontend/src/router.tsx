import { createBrowserRouter } from "react-router";
import Root from "./routes/root";
import Home from "./routes/home";
import Organizations from "./routes/organizations";
import Login from "./routes/login";

export const router = createBrowserRouter([
  {
    path: "/",
    Component: Root,
    children: [
      { index: true, Component: Home },
      { path: "organizations", Component: Organizations },
      { path: "login", Component: Login },
    ],
  },
]);
