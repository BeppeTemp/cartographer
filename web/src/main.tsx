import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/components.css";

const container = document.getElementById("root");
if (!container) throw new Error("#root is missing from the shell");

createRoot(container).render(
  <StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
