import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@warehouse/ui-kit/tokens.css";
import App from "./App";

/** Standalone dev entry -- lets order_mgmt_mfe run and be worked on in
 *  isolation (npm run dev, :5181) without the shell attached. The shell
 *  imports App.tsx directly via Module Federation and provides its own
 *  chrome; this file is dev-only scaffolding. No BrowserRouter here since
 *  this remote (like labor-mfe/process-path-mfe) has no internal
 *  sub-routes -- it is a single screen. */
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <div style={{ padding: "var(--wh-space-5)" }}>
      <App />
    </div>
  </StrictMode>,
);
