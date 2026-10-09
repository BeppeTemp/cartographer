import { useEffect, useState } from "react";
import { fetchServerVersion } from "../api/client";
import type { KBSummary } from "../api/types";
import type { Theme } from "../lib/theme";
import { Icon, type IconName } from "./Icon";
import { KBPicker } from "./KBPicker";

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
  /** The brand leads back to the atlas: the graph, nothing selected. */
  onHome?(): void;
  onOpenNav?(): void;
  onOpenInspector?(): void;
}

const THEME_ICON: Record<Theme, IconName> = {
  dark: "moon",
  light: "sun",
  system: "auto",
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
  onHome,
  onOpenNav,
  onOpenInspector,
}: Props) {
  const nextTheme: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };
  const [version, setVersion] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    void fetchServerVersion(controller.signal).then(setVersion);
    return () => controller.abort();
  }, []);

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
          <Icon name="list" />
        </button>
      )}
      <button type="button" className="topbar__brand" onClick={onHome} aria-label="Cartographer — back to the atlas">
        {/* The brand's master SVGs (docs/brand, D232), one per theme; CSS shows
            the one for the resolved theme, so a switch never waits on a load. */}
        <img
          className="topbar__mark topbar__mark--light"
          src={`${import.meta.env.BASE_URL}brand/coordinate-pine.svg`}
          alt=""
          width={28}
          height={28}
        />
        <img
          className="topbar__mark topbar__mark--dark"
          src={`${import.meta.env.BASE_URL}brand/coordinate-sage.svg`}
          alt=""
          width={28}
          height={28}
        />
        <span className="topbar__name">Cartographer</span>
      </button>

      {kbs.length > 0 && (
        <KBPicker kbs={kbs} activeKB={activeKB} onChange={onKBChange} />
      )}

      <button type="button" className="topbar__search field" onClick={onOpenPalette} aria-label="Search concepts">
        <Icon name="search" size={16} />
        <span className="field__placeholder">Search concepts</span>
        <kbd className="kbd">Ctrl K</kbd>
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
            <Icon name="info" />
          </button>
        )}
        {/* Being connected is the normal state and says nothing: the status
            takes room only when the server is unreachable. The live region
            stays mounted so the change is still announced. */}
        <span
          className={`topbar__status topbar__status--${offline ? "offline" : "online"}`}
          role="status"
          hidden={!offline}
        >
          <span aria-hidden="true">●</span>
          <span className="topbar__status-text">{offline ? "Disconnected" : "Connected"}</span>
        </span>
        {version && (
          <span className="topbar__version" title="Server version">
            {/^\d/.test(version) ? `v${version}` : version}
          </span>
        )}
        <button
          type="button"
          className="button button--icon"
          onClick={() => onThemeChange(nextTheme[theme])}
          aria-label={`Theme: ${theme}. Switch to ${nextTheme[theme]}.`}
          title={`Theme: ${theme}`}
        >
          <Icon name={THEME_ICON[theme]} />
        </button>
      </div>
    </header>
  );
}
