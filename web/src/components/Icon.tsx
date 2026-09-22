/**
 * The Atlas's line icons: 24x24, 1.5px stroke in currentColor, round caps --
 * the lineal family of the brand kit, drawn here rather than taken from an
 * icon set whose licence the project would have to carry. Decorative by
 * default: the control that holds one carries the accessible name.
 */
export type IconName =
  | "atlas"
  | "observatory"
  | "panel-open"
  | "panel-close"
  | "list"
  | "motion"
  | "pause"
  | "search"
  | "chevron"
  | "sun"
  | "moon"
  | "auto"
  | "info"
  | "plus"
  | "minus"
  | "fit"
  | "relax";

const PATHS: Record<IconName, string> = {
  // Three places and the routes between them.
  atlas: "M6.5 7.5 L16.5 5.5 M6.5 7.5 L10 17 M16.5 5.5 L10 17 M16.5 5.5 L19 13",
  // A pulse over a baseline: the health of the atlas.
  observatory: "M3 12h4l2-5 4 10 2-5h6",
  "panel-open": "M4 5h16v14H4z M9 5v14 M13 10l2 2-2 2",
  "panel-close": "M4 5h16v14H4z M9 5v14 M15 10l-2 2 2 2",
  list: "M9 7h11 M9 12h11 M9 17h11 M4.5 7h.01 M4.5 12h.01 M4.5 17h.01",
  motion: "M8 5.5v13l10.5-6.5z",
  pause: "M8.5 5.5v13 M15.5 5.5v13",
  search: "M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13z M15.5 15.5 20 20",
  chevron: "M8 10l4 4 4-4",
  sun: "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8z M12 2.5v2 M12 19.5v2 M4.6 4.6l1.4 1.4 M18 18l1.4 1.4 M2.5 12h2 M19.5 12h2 M4.6 19.4 6 18 M18 6l1.4-1.4",
  moon: "M19.5 14.5A7.5 7.5 0 0 1 9.5 4.5a7.5 7.5 0 1 0 10 10z",
  // Half light, half dark: follow the system.
  auto: "M12 20a8 8 0 1 0 0-16 8 8 0 0 0 0 16z M12 4v16",
  info: "M12 20a8 8 0 1 0 0-16 8 8 0 0 0 0 16z M12 11v5 M12 8h.01",
  plus: "M12 6v12 M6 12h12",
  minus: "M6 12h12",
  fit: "M4 9V4h5 M15 4h5v5 M20 15v5h-5 M9 20H4v-5",
  // Arrows round a point: let the layout settle again.
  relax: "M19 12a7 7 0 1 1-2.05-4.95 M19 4v3.5h-3.5",
};

const DOTS: Partial<Record<IconName, [number, number, number][]>> = {
  atlas: [
    [6.5, 7.5, 2],
    [16.5, 5.5, 2],
    [10, 17, 2],
    [19, 13, 1.5],
  ],
};

export function Icon({ name, size = 18 }: { name: IconName; size?: number }) {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={PATHS[name]} />
      {DOTS[name]?.map(([cx, cy, r]) => (
        <circle key={`${cx},${cy}`} cx={cx} cy={cy} r={r} fill="var(--surface-1)" />
      ))}
    </svg>
  );
}
