import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// order_mgmt_mfe: the order-management remote. Exposes ./App -- the shell
// (warehouse-console) lazy-loads it at /order-management. Also runnable
// standalone on :5181 for local development without the shell (see
// main.tsx). Port 5181 and the remote name "order_mgmt_mfe" are fixed by
// warehouse-console's own vite.config.ts/App.tsx, which already points at
// http://localhost:5181/remoteEntry.js and lazy-imports "order_mgmt_mfe/App"
// -- this repo must match those exactly, not choose its own.
export default defineConfig({
  plugins: [
    react(),
    federation({
      name: "order_mgmt_mfe",
      filename: "remoteEntry.js",
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.2.8" },
        "react-dom": { singleton: true, requiredVersion: "^19.2.8" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 5181,
    strictPort: true,
    cors: true,
    origin: "http://localhost:5181",
  },
  preview: {
    port: 5181,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
