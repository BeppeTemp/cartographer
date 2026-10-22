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
  title?: ReactNode;
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
        {title && <h1 className="page__title">{title}</h1>}
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
        <button
          key={v}
          type="button"
          className="segmented__option"
          aria-pressed={value === v}
          onClick={() => onChange(v)}
        >
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
export function Hero({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={className ? `hero ${className}` : "hero"} role="region" aria-label={label}>
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
export function Figures({
  items,
}: {
  items: { label: string; value: number | string; tone?: string; sign?: string }[];
}) {
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
    <LoadingRegion label={label} className="skeleton-rows">
      {Array.from({ length: rows }, (_, i) => (
        <span key={i} className="skeleton" />
      ))}
    </LoadingRegion>
  );
}

/**
 * The one live region of a loading page: a single status with a screen-reader
 * label, and the shapes inside it hidden, so nothing is announced twice.
 */
export function LoadingRegion({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={className ? `loading-shape ${className}` : "loading-shape"} role="status" aria-busy="true">
      <span className="sr-only">{label}</span>
      <div className="loading-shape__body" aria-hidden="true">
        {children}
      </div>
    </div>
  );
}

/** A grey bar of a given size: a line of text, a pill, an avatar before they exist. */
export function Bone({
  w,
  h,
  round,
  line,
  className,
}: {
  w?: string | number;
  h?: string | number;
  round?: boolean;
  /** A line of text: placed inside the element whose text it stands for, it
   *  takes that element's font and line height, so the shape is the height of
   *  the content it gives way to. */
  line?: boolean;
  className?: string;
}) {
  return (
    <span
      className={`skeleton bone${round ? " bone--round" : ""}${line ? " bone--line" : ""}${className ? ` ${className}` : ""}`}
      aria-hidden="true"
      style={{ width: w, height: h }}
    />
  );
}

/** A page headline that has not arrived: a bar the size of the title line, with the label for screen readers only. */
export function TitleBone({ label }: { label: string }) {
  return (
    <>
      <span className="skeleton skel-title" aria-hidden="true" />
      <span className="sr-only">{label}</span>
    </>
  );
}

/** A page's subtitle that has not arrived: one line of the subtitle's own text size. */
export function SubtitleBone({ w = "42ch" }: { w?: string | number }) {
  return <Bone line w={w} className="bone--subtitle" />;
}

/**
 * The hero's rows before their chips exist: each row its label and a line of
 * pill-shaped bones, the first optionally over a bar (Where's map bar).
 */
export function HeroBones({ rows, bar = false }: { rows: number[]; bar?: boolean }) {
  return (
    <div className="hero">
      {rows.map((chips, r) => (
        <div key={r} className="hero__row">
          <span className="hero__label">
            <Bone line w={44} />
          </span>
          <div className="hero__content">
            {bar && r === 0 && <Bone w="100%" h={4} />}
            <span className="filter-chips">
              {Array.from({ length: chips }, (_, i) => (
                <Bone key={i} w={70 + ((i * 23 + r * 11) % 50)} h={26} round />
              ))}
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}

/** Activity: the summary hero (figures and histogram), then a day with timeline entries. */
export function ActivitySkeleton({ label }: { label: string }) {
  return (
    <LoadingRegion label={label}>
      <div className="hero activity-hero">
        <div className="activity-sum">
          <div className="activity-sum__lead">
            <div className="activity-stats">
              <Bone line w={280} />
            </div>
            <span className="activity-top">
              <span className="activity-top__label">
                <Bone line w={260} />
              </span>
            </span>
          </div>
          <div className="activity-bars">
            <div className="activity-bars__track">
              <Bone w="100%" h="100%" />
            </div>
            <span className="activity-bars__caption">
              <Bone line w="60%" />
            </span>
          </div>
        </div>
      </div>
      {[4, 3].map((n, d) => (
        <div key={d} className="timeline__day">
          <h2 className="timeline__date">
            <span>
              <Bone line w={96} />
            </span>
          </h2>
          <ol className="timeline__entries">
            {Array.from({ length: n }, (_, i) => (
              <li key={i} className="timeline__entry">
                <div className="timeline__item">
                  <span className="timeline__time">
                    <Bone line w="5ch" />
                  </span>
                  <span className="timeline__track">
                    <span className="timeline__node" />
                  </span>
                  <span className="timeline__item-body">
                    <span className="timeline__item-head">
                      <span className="timeline__title" style={{ width: `${38 + ((i * 17 + d * 9) % 30)}%` }}>
                        <Bone line w="100%" />
                      </span>
                    </span>
                    <span className="timeline__reason">
                      <Bone line w={`${22 + ((i * 11) % 25)}%`} />
                    </span>
                  </span>
                  <span className="timeline__authors">
                    <Bone w={22} h={22} round />
                  </span>
                </div>
              </li>
            ))}
          </ol>
        </div>
      ))}
    </LoadingRegion>
  );
}

/** Work: board columns with cards (map or status, title, pills). */
export function WorkSkeleton({ label }: { label: string }) {
  return (
    <LoadingRegion label={label}>
      <HeroBones rows={[9, 8]} bar />
      <div className="work__board">
        {[4, 3, 4, 2, 3].map((n, c) => (
          <section key={c} className="work__column">
            <h2 className="work__column-title">
              <Bone line w={90} />
            </h2>
            <ul className="work__cards">
              {Array.from({ length: n }, (_, i) => (
                <li key={i}>
                  <div className="work__card">
                    <div className="work__card-main">
                      <span className="work__card-map">
                        <Bone line w={64} />
                      </span>
                      <span className="work__card-title">
                        <Bone line w="92%" />
                        <br />
                        <Bone line w={`${40 + ((i * 13 + c * 7) % 40)}%`} />
                      </span>
                      <span className="work__card-meta">
                        <Bone w={44} h={20} round />
                        <Bone line w={28} />
                      </span>
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </LoadingRegion>
  );
}

/** Health: an agenda category with its issue rows (label, bar, count). */
export function HealthSkeleton({ label }: { label: string }) {
  return (
    <LoadingRegion label={label}>
      <div className="agenda-list">
        {[4, 2].map((n, g) => (
          <details key={g} className="agenda" open>
            <summary className="agenda__head">
              <Bone w={10} h={10} round />
              <Bone w={130} h={16} />
              <Bone w={60} h={12} />
            </summary>
            <ul className="agenda__rows">
              {Array.from({ length: n }, (_, i) => (
                <li key={i} className="arow-item">
                  <div className="arow">
                    <span className="arow__label">
                      <Bone w={`${60 + ((i * 23 + g * 11) % 40)}%`} h={14} />
                    </span>
                    <span className="arow__bar">
                      <Bone w={`${20 + ((i * 29) % 70)}%`} h={8} />
                    </span>
                    <span className="arow__count">
                      <Bone w={56} h={14} />
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </details>
        ))}
      </div>
    </LoadingRegion>
  );
}

/** Retired: rows with a title, a pill, and a meta line. */
export function RetiredSkeleton({ label }: { label: string }) {
  return (
    <LoadingRegion label={label}>
      <HeroBones rows={[2]} />
      <ul className="retired__list">
        {[0, 1, 2, 3].map((i) => (
          <li key={i}>
            <div className="retired__row">
              <div className="retired__head">
                <Bone w={`${30 + ((i * 19) % 30)}%`} h={16} />
                <Bone w={64} h={18} />
              </div>
              <div className="retired__meta">
                <Bone w={`${35 + ((i * 13) % 25)}%`} h={12} />
              </div>
              <Bone w="70%" h={12} />
            </div>
          </li>
        ))}
      </ul>
    </LoadingRegion>
  );
}

/** Artifacts: kind headings, each with a grid of tiles (name, two lines, foot). */
export function CatalogSkeleton({
  label,
  groups = 2,
  tiles = 6,
  hero,
}: {
  label: string;
  groups?: number;
  tiles?: number;
  /** The chips of each hero row, when the page has a hero above its catalog. */
  hero?: number[];
}) {
  return (
    <LoadingRegion label={label} className="catalog">
      {hero && <HeroBones rows={hero} />}
      {Array.from({ length: groups }, (_, g) => (
        <section key={g} className="catalog__group">
          <header className="catalog__group-head">
            <h2 className="catalog__group-title">
              <Bone w={140} h={16} />
            </h2>
            <p className="catalog__about">
              <Bone w="50%" h={12} />
            </p>
          </header>
          <ul className="catalog__tiles">
            {Array.from({ length: g === 0 ? tiles : Math.ceil(tiles / 2) }, (_, i) => (
              <li key={i}>
                <div className="catalog__tile">
                  <Bone w={`${45 + ((i * 17) % 35)}%`} h={16} />
                  <span className="catalog__tile-desc">
                    <Bone w="95%" h={12} />
                    <Bone w="70%" h={12} />
                  </span>
                  <Bone w={80} h={12} />
                </div>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </LoadingRegion>
  );
}

/** Settings: a group heading and a bordered block of rows (key and description left, value pill right). */
export function SettingsSkeleton({ label, rows = 5 }: { label: string; rows?: number }) {
  return (
    <LoadingRegion label={label}>
      <section className="settings__group">
        <header className="settings__group-head">
          <h2 className="settings__heading">
            <Bone w={22} h={22} />
            <Bone w={120} h={16} />
          </h2>
          <Bone w={60} h={12} />
        </header>
        <ul className="settings__rows">
          {Array.from({ length: rows }, (_, i) => (
            <li key={i} className="settings__row">
              <div className="settings__row-head settings__row-head--static">
                <span className="settings__what">
                  <Bone w={`${110 + ((i * 37) % 90)}px`} h={14} />
                  <Bone w={`${55 + ((i * 17) % 30)}%`} h={12} />
                </span>
                <span className="settings__val">
                  <Bone w={`${70 + ((i * 29) % 80)}px`} h={22} />
                </span>
              </div>
            </li>
          ))}
        </ul>
      </section>
    </LoadingRegion>
  );
}

/** Templates: the cards of the types, a name and count over a brief, fields and numbered sections. */
export function TemplatesSkeleton({ label, cards = 6 }: { label: string; cards?: number }) {
  return (
    <LoadingRegion label={label}>
      <ul className="tpl-cards">
        {Array.from({ length: cards }, (_, i) => (
          <li key={i}>
            <div className="tpl-card">
              <div className="tpl-card__head">
                <span className="tpl-card__name">
                  <Bone line w={`${40 + ((i * 17) % 30)}%`} />
                </span>
                <Bone w={84} h={22} round />
              </div>
              <span className="tpl-card__usage" />
              <div className="tpl-card__body">
                <p className="tpl-card__brief">
                  <Bone line w="80%" />
                </p>
                <span className="tpl-card__fields">
                  <Bone w={44} h={18} />
                  <Bone w={60} h={18} />
                </span>
                <ol className="tpl-card__sections">
                  {Array.from({ length: 4 + (i % 3) }, (_, k) => (
                    <li key={k}>
                      <Bone line w={`${30 + ((k * 23 + i * 7) % 40)}%`} />
                    </li>
                  ))}
                </ol>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </LoadingRegion>
  );
}

/** A source file that has not arrived: the frame Settings and Instructions show files in, with numbered lines. */
export function SourceSkeleton({ label, lines = 14 }: { label: string; lines?: number }) {
  const widths = [8, 0, 34, 0, 82, 76, 40, 0, 26, 0, 88, 70, 0, 30, 64, 52];
  return (
    <LoadingRegion label={label}>
      <figure className="settings__file">
        <figcaption className="settings__file-head">
          <span className="settings__file-name">
            <Bone line w={120} />
          </span>
        </figcaption>
        <pre className="settings__yaml">
          <code>
            {Array.from({ length: lines }, (_, i) => (
              <span key={i} className="yaml__line">
                {widths[i % widths.length] ? <Bone line w={`${widths[i % widths.length]}%`} /> : " "}
                {"\n"}
              </span>
            ))}
          </code>
        </pre>
      </figure>
    </LoadingRegion>
  );
}

/**
 * A body of text that has not arrived: paragraphs of line bones inside the
 * concept reader's own box (.markdown), so each line is the height of a line
 * of the text that replaces it.
 */
export function TextSkeleton({ label, lines = 6 }: { label: string; lines?: number }) {
  const widths = [96, 88, 92, 60, 94, 80, 90, 45];
  const paragraphs: number[][] = [];
  for (let i = 0; i < lines; i += 4) paragraphs.push(Array.from({ length: Math.min(4, lines - i) }, (_, k) => i + k));
  return (
    <LoadingRegion label={label} className="text-skeleton">
      <div className="markdown">
        {paragraphs.map((p, n) => (
          <p key={n}>
            {p.map((i) => (
              <span key={i} className="text-skeleton__line">
                <Bone line w={`${widths[i % widths.length]}%`} />
              </span>
            ))}
          </p>
        ))}
      </div>
    </LoadingRegion>
  );
}

/** A side panel list (releases, concept revisions): entries of a title line and a meta line. */
export function EntriesSkeleton({ label, rows = 4 }: { label: string; rows?: number }) {
  return (
    <LoadingRegion label={label} className="entries-skeleton">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="entries-skeleton__row">
          <Bone w={`${45 + ((i * 23) % 40)}%`} h={16} />
          <Bone w={`${25 + ((i * 17) % 25)}%`} h={12} />
        </div>
      ))}
    </LoadingRegion>
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

/**
 * Tabs between the pages of one section (Artifacts and Secrets): the same
 * strip as the reading panel's tabs, under the page header.
 */
export function PageTabs<T extends string>({
  label,
  tabs,
  current,
  onSelect,
}: {
  label: string;
  tabs: [T, string][];
  current: T;
  onSelect(tab: T): void;
}) {
  return (
    <div className="inspector__tabs page__tabs" role="tablist" aria-label={label}>
      {tabs.map(([id, name]) => (
        <button
          key={id}
          type="button"
          role="tab"
          aria-selected={id === current}
          className="inspector__tab"
          onClick={() => onSelect(id)}
        >
          {name}
        </button>
      ))}
    </div>
  );
}
