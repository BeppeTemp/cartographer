import type { KBSummary } from "../api/types";
import type { Theme } from "../lib/theme";

interface Props {
  kbs: KBSummary[];
  activeKB: string | null;
  theme: Theme;
  offline: boolean;
  /** Below 1,024px: navigation and the inspector live in sheets this bar
   *  opens. */
  narrow?: boolean;
  hasSelection?: boolean;
  onKBChange(name: string): void;
  onThemeChange(theme: Theme): void;
  onOpenPalette(): void;
  onOpenNav?(): void;
  onOpenInspector?(): void;
}

const THEME_GLYPH: Record<Theme, string> = {
  dark: "◓",
  light: "○",
  system: "◑",
};

export function TopBar({
  kbs,
  activeKB,
  theme,
  offline,
  narrow = false,
  hasSelection = false,
  onKBChange,
  onThemeChange,
  onOpenPalette,
  onOpenNav,
  onOpenInspector,
}: Props) {
  const nextTheme: Record<Theme, Theme> = { dark: "light", light: "system", system: "dark" };

  return (
    <header className="topbar">
      {narrow && (
        <button
          type="button"
          className="button button--icon"
          onClick={onOpenNav}
          aria-label="Open navigation"
          aria-haspopup="dialog"
        >
          <span aria-hidden="true">&#9776;</span>
        </button>
      )}
      <div className="topbar__brand">
        <span className="topbar__mark" aria-hidden="true" />
        <span className="topbar__name">Cartographer</span>
      </div>

      {kbs.length > 0 && (
        <label className="topbar__kb">
          <span className="sr-only">Knowledge Base</span>
          <select
            className="input"
            value={activeKB ?? ""}
            onChange={(event) => onKBChange(event.target.value)}
          >
            {kbs.map((kb) => (
              <option key={kb.name} value={kb.name}>
                {kb.name}
                {kb.ready ? "" : " (degraded)"}
              </option>
            ))}
          </select>
        </label>
      )}

      <button type="button" className="topbar__search" onClick={onOpenPalette}>
        <span aria-hidden="true">&#9906;</span>
        <span>Search concepts</span>
        <kbd className="kbd">Ctrl</kbd>
        <kbd className="kbd">K</kbd>
      </button>

      <div className="topbar__end">
        {narrow && hasSelection && (
          <button
            type="button"
            className="button button--icon"
            onClick={onOpenInspector}
            aria-label="Open inspector"
            aria-haspopup="dialog"
          >
            <span aria-hidden="true">&#9432;</span>
          </button>
        )}
        <span
          className={`topbar__status topbar__status--${offline ? "offline" : "online"}`}
          role="status"
        >
          <span aria-hidden="true">{offline ? "●" : "●"}</span>
          {offline ? "Disconnected" : "Connected"}
        </span>
        <button
          type="button"
          className="button button--icon"
          onClick={() => onThemeChange(nextTheme[theme])}
          aria-label={`Theme: ${theme}. Switch to ${nextTheme[theme]}.`}
          title={`Theme: ${theme}`}
        >
          <span aria-hidden="true">{THEME_GLYPH[theme]}</span>
        </button>
      </div>
    </header>
  );
}
