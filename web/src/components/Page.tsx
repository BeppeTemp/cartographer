import type { CSSProperties, ReactNode } from "react";
import { collectionVar } from "../lib/palette";

/**
 * The page kit: the pieces every reading panel (Activity, Work, Health) is
 * built from, so they read as one app rather than three documents. A page is
 * a header that says the answer in words, a band that sums it up side by side,
 * then sections of dense rows. The kit owns the layout and the type; a page
 * owns only what it says.
 */

/** The page itself: one width, one rhythm, its own scroll. */
export function Page({
  label,
  busy,
  className,
  children,
}: {
  label: string;
  busy?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `page ${className}` : "page"} aria-label={label} aria-busy={busy || undefined}>
      {children}
    </section>
  );
}

/**
 * The eyebrow names the page, the title answers it — a sentence with its
 * count in bold — and the actions sit on the right. Long explanations go in
 * `help`, folded behind a "?" so they never push the answer down.
 */
export function PageHeader({
  eyebrow,
  title,
  subtitle,
  actions,
  help,
}: {
  eyebrow: string;
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  help?: ReactNode;
}) {
  return (
    <header className="page__head">
      <div className="page__heading">
        <p className="page__eyebrow">
          {eyebrow}
          {help && (
            <details className="page__help">
              <summary aria-label={`About ${eyebrow}`} title={`About ${eyebrow}`}>
                ?
              </summary>
              <div className="page__help-body">{help}</div>
            </details>
          )}
        </p>
        <h1 className="page__title">{title}</h1>
        {subtitle && <p className="page__subtitle">{subtitle}</p>}
      </div>
      {actions && <div className="page__actions">{actions}</div>}
    </header>
  );
}

/** A count set in bold inside a title sentence. */
export function Count({ children }: { children: ReactNode }) {
  return <strong className="page__count">{children}</strong>;
}

/** A segmented switch: one choice among a few, pressed state as the value. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly (readonly [T, string])[];
  onChange(value: T): void;
}) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      {options.map(([v, text]) => (
        <button key={v} type="button" className="segmented__option" aria-pressed={value === v} onClick={() => onChange(v)}>
          {text}
        </button>
      ))}
    </div>
  );
}

/** A titled block: an overview section, a card. */
export function Facet({
  title,
  id,
  className,
  children,
}: {
  title?: string;
  id?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `facet ${className}` : "facet"} aria-labelledby={title ? id : undefined}>
      {title && (
        <h2 id={id} className="facet__title">
          {title}
        </h2>
      )}
      {children}
    </section>
  );
}

/**
 * The open band at the top of a page: no box around it, rows of numbers, a
 * chart and filters, separated by space and a hairline below. Boxes are kept
 * for content that is dense enough to fill them.
 */
export function Hero({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="hero" role="region" aria-label={label}>
      {children}
    </div>
  );
}

/** One labelled line of the hero: "Where", "Who", "Status"… */
export function HeroRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="hero__row">
      <span className="hero__label">{label}</span>
      <div className="hero__content">{children}</div>
    </div>
  );
}

/** Big numbers set in a line: the band's headline figures. */
export function Figures({ items }: { items: { label: string; value: number | string; tone?: string; sign?: string }[] }) {
  return (
    <dl className="figures">
      {items.map((s) => (
        <div key={s.label} className="figures__item" data-tone={s.tone}>
          <dt>{s.label}</dt>
          <dd>
            {s.sign && <span className="figures__sign">{s.sign}</span>}
            {s.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** A wrapping line of filter chips. */
export function FilterChips({ label, children }: { label: string; children: ReactNode }) {
  return (
    <ul className="filter-chips" aria-label={label}>
      {children}
    </ul>
  );
}

/**
 * A filter as a pill: a mark, a name and a count. Pressed means "only this";
 * pressing it again clears the filter. The Map's colour, when it has one,
 * lights its edge when pressed.
 */
export function FilterChip({
  label,
  count,
  pressed,
  hue,
  mark,
  onToggle,
}: {
  label: string;
  count: number;
  pressed: boolean;
  hue?: string;
  mark?: ReactNode;
  onToggle(): void;
}) {
  return (
    <li>
      <button
        type="button"
        className="filter-chip"
        aria-pressed={pressed}
        style={hue ? ({ "--map": hue } as CSSProperties) : undefined}
        onClick={onToggle}
      >
        {mark}
        <span className="filter-chip__name">{label}</span>
        <span className="filter-chip__count">{count}</span>
      </button>
    </li>
  );
}

/** A Map's colour as a dot. */
export function MapDot({ map }: { map?: string }) {
  return (
    <span
      className="map-dot"
      aria-hidden="true"
      style={map !== undefined ? ({ "--map": collectionVar(map || "(root)") } as CSSProperties) : undefined}
    />
  );
}

/** A Map's share of the whole: one segment per Map, the filtered one lit. */
export function MapBar({ maps, active }: { maps: [string, number][]; active: string | null }) {
  return (
    <div className="map-bar" aria-hidden="true">
      {maps.map(([name, count]) => (
        <span
          key={name}
          className="map-bar__seg"
          data-dim={active !== null && active !== name}
          style={{ flexGrow: count, "--map": collectionVar(name || "(root)") } as CSSProperties}
        />
      ))}
    </div>
  );
}

/** Initials on a hue the name picks: the same person, the same disc. */
export function Avatar({ name, size }: { name: string; size?: "small" }) {
  const initials = name
    .split(/[\s._-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]!.toUpperCase())
    .join("");
  return (
    <span
      className={size ? `avatar avatar--${size}` : "avatar"}
      aria-hidden="true"
      style={{ "--map": collectionVar(name) } as CSSProperties}
    >
      {initials || "?"}
    </span>
  );
}

/** A titled block of the page, with its count and its own controls. */
export function PageSection({
  title,
  id,
  count,
  actions,
  className,
  children,
}: {
  title: string;
  id: string;
  count?: number;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `page-section ${className}` : "page-section"} aria-labelledby={id}>
      <header className="page-section__head">
        <h2 id={id} className="page-section__title">
          {title}
          {count !== undefined && <span className="page-section__count">{count}</span>}
        </h2>
        {actions && <div className="page-section__actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

/**
 * A short, positive line where a list would be: "nothing is wrong" is news,
 * said in one line with a check, not a page left empty.
 */
export function Quiet({ children, tone = "ok" }: { children: ReactNode; tone?: "ok" | "neutral" }) {
  return (
    <p className={`quiet quiet--${tone}`}>
      <span className="quiet__glyph" aria-hidden="true">
        {tone === "ok" ? "✓" : "·"}
      </span>
      <span>{children}</span>
    </p>
  );
}

/** Placeholder rows while a list loads. */
export function SkeletonRows({ label, rows = 4 }: { label: string; rows?: number }) {
  return (
    <div className="skeleton-rows" role="status" aria-busy="true" aria-label={label}>
      {Array.from({ length: rows }, (_, i) => (
        <span key={i} className="skeleton" />
      ))}
    </div>
  );
}

/** "today", "yesterday", "3 days ago": a date said the way a person would. */
export function relativeDay(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return iso;
  const start = (x: number) => {
    const d = new Date(x);
    d.setHours(0, 0, 0, 0);
    return d.getTime();
  };
  const days = Math.round((start(now) - start(t)) / 86_400_000);
  if (days === 0) return "today";
  if (days === 1) return "yesterday";
  if (days === -1) return "tomorrow";
  if (days < 0) return `in ${-days} days`;
  return `${days} days ago`;
}
