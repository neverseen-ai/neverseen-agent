import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// The console is the agent's /settings page. The build writes the two files the agent
// embeds — console.js and console.css, in internal/proxy/console/ — and the page around
// them is internal/proxy/settings.html, a Go template, because the control key goes
// into the page there and nowhere else.
//
// Fixed names, one chunk, no preload polyfill: the agent inlines both files into one
// response served under `default-src 'none'`, so there is no second request to make.
// Both files are committed, so `go build` needs no Node; CI rebuilds them and fails on
// any difference.
const OUT = "../internal/proxy/console";

// index.html is the dev server's entry, not the agent's page: dropped from the build.
function withoutHTML(): Plugin {
  return {
    name: "neverseen-without-html",
    // After Vite's own HTML plugin, which is the one that adds the file.
    enforce: "post",
    generateBundle(_, bundle) {
      for (const name of Object.keys(bundle)) if (name.endsWith(".html")) delete bundle[name];
    },
  };
}

// `npm run dev` works against the agent running on this machine, so the page can be
// changed with hot reload and real answers. The proxy adds nothing: the page sends the
// key itself, asked for once (client.ts). A proxy adding the key would hand it to every
// caller of the dev server — another account's included — and switch masking off for
// anyone who asked, ticket or not.
export default defineConfig(() => {
  const route = { target: "http://127.0.0.1:9787", changeOrigin: true };
  return {
    server: {
      host: "127.0.0.1",
      port: 9245,
      strictPort: true,
      proxy: { "/healthz": route, "/policy": route, "/today": route, "/test": route, "/enrol": route, "/report": route, "/rtk": route },
    },
    plugins: [react(), withoutHTML()],
    build: {
      outDir: OUT,
      emptyOutDir: true,
      modulePreload: { polyfill: false },
      cssCodeSplit: false,
      rollupOptions: {
        output: { entryFileNames: "console.js", assetFileNames: "console[extname]", codeSplitting: false },
      },
    },
  };
});
