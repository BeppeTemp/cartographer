import type { IconName } from "../components/Icon";

/**
 * The closed lists a dashboard widget draws from. The Go side holds the same
 * lists (internal/kb/dashboard.go) and `dashboardlists.test.ts` fails on any
 * drift: an icon is a name of the Atlas icon set and an accent a name of its
 * colour tokens, never a colour (D227).
 */
export const DASHBOARD_ICONS = [
  "list",
  "work",
  "health",
  "maps",
  "sources",
  "services",
  "artifacts",
  "components",
  "key",
  "paths",
  "eye",
  "lock",
  "alert",
  "person",
  "check",
  "info",
  "activity",
  "search",
  "grow",
  "settings",
] as const satisfies readonly IconName[];

export const DASHBOARD_ACCENTS = ["neutral", "primary", "accent", "clay", "ok", "info", "warning", "error"] as const;

/** The CSS colour of an accent; an unknown name (a newer server) is neutral. */
export function accentColor(accent: string | undefined): string {
  switch (accent) {
    case "primary":
      return "var(--primary)";
    case "accent":
      return "var(--accent)";
    case "clay":
      return "var(--clay)";
    case "ok":
      return "var(--sev-ok)";
    case "info":
      return "var(--sev-info)";
    case "warning":
      return "var(--sev-warning)";
    case "error":
      return "var(--sev-error)";
    default:
      return "var(--text-muted)";
  }
}

/** The icon a widget names, or null for none or a name this Atlas does not know. */
export function widgetIcon(icon: string | undefined): IconName | null {
  return icon && (DASHBOARD_ICONS as readonly string[]).includes(icon) ? (icon as IconName) : null;
}
