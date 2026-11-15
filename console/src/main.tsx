import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./app/App";
import { StoreProvider } from "./app/store";
import { ToastProvider } from "./ui/overlays";
import "./styles.css";
import "./screens.css";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <ToastProvider>
      <StoreProvider>
        <App />
      </StoreProvider>
    </ToastProvider>
  </React.StrictMode>,
);
