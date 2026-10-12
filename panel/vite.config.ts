import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The dev server proxies /v1 to the API so the panel is same-origin in development and
// in the container: no CORS configuration to get wrong, and the token never travels
// further than the browser it was typed into.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/v1": { target: process.env.LIVEOPS_API ?? "http://localhost:18100", changeOrigin: true } },
  },
});
