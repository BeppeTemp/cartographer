// Resolves the theme before first paint, so the page never flashes the wrong
// palette while the bundle loads (D233). A classic same-origin script on
// purpose: the CSP allows no inline script (internal/webui/webui.go). It
// mirrors readTheme/applyTheme in src/lib/theme.ts, which own the theme once
// the app runs; keep the storage key in step.
(function () {
  var theme = "system";
  try {
    var stored = localStorage.getItem("cartographer.theme");
    if (stored === "light" || stored === "dark" || stored === "system") theme = stored;
  } catch (e) {
    // Storage disabled: the system decides.
  }
  if (theme === "system") {
    theme = window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  }
  document.documentElement.dataset.theme = theme;
})();
