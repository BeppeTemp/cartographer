import { useEffect, useRef, useState, type RefObject } from "react";
import { fetchReleases } from "../api/client";
import type { ReleaseEntry, ReleasesView } from "../api/types";
import { Icon } from "./Icon";
import { Markdown } from "./Markdown";
import { Sheet } from "./Sheet";
import { ErrorState, Skeleton } from "./States";

/** Versions are shown with their v, whichever way the server spelled them. */
const tag = (version: string) => (/^\d/.test(version) ? `v${version}` : version);

/**
 * A link target that came from the network (GitHub, through the server) is
 * followed only when it is https: a `javascript:` URL in a response must never
 * become a clickable href.
 */
function safeURL(url: string | undefined): string {
  return url && /^https:\/\//i.test(url) ? url : "";
}

/**
 * The body without its first heading when that heading only restates the
 * release: release-please opens each changelog with
 * `## [0.24.0](compare-url) (date)`, and the section already carries the
 * version (linked to the release) and the date.
 */
export function withoutTitleLine(release: ReleaseEntry): string {
  const escaped = release.version.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return release.body.replace(new RegExp(`^\\s*#{1,2}\\s+\\[?v?${escaped}\\b[^\\n]*\\n?`), "").replace(/^\s+/, "");
}

/**
 * The release history (D862): every release with its changelog, newest first,
 * the one this server runs marked, and, when a newer one is known, how to
 * upgrade. The changelogs are GitHub release bodies written by anyone with
 * push access: they are rendered as untrusted Markdown (no raw HTML, no remote
 * images) and never as markup.
 */
export function Releases({ onClose }: { onClose(): void }) {
  const [view, setView] = useState<ReleasesView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const currentRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setError(null);
    fetchReleases(controller.signal)
      .then(setView)
      .catch((err: unknown) => {
        if (!controller.signal.aborted) setError(err);
      });
    return () => controller.abort();
  }, [attempt]);

  // Newer releases precede the current one: bring it into view, without
  // moving at all when it already is, so a single newer release keeps the
  // upgrade block on screen.
  const currentIndex = view?.releases.findIndex((r) => r.current) ?? -1;
  useEffect(() => {
    if (currentIndex > 0) currentRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [view, currentIndex]);

  const releasesURL = safeURL(view?.releases_url);

  return (
    <Sheet side="end" label="Releases" wide onClose={onClose}>
      <div className="releases">
        <header className="releases__head">
          <h2 className="releases__title">Releases</h2>
          {view?.current && <p className="releases__subtitle">This server runs {tag(view.current)}</p>}
        </header>

        {error ? (
          <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
        ) : !view ? (
          <Skeleton lines={6} label="Loading releases" />
        ) : (
          <>
            {view.upgrade_command && view.latest && (
              <UpgradeBlock latest={view.latest} command={view.upgrade_command} />
            )}
            {view.unavailable && (
              <p className="releases__notice" role="status">
                {view.unavailable === "disabled"
                  ? "The update check is off on this server, so the release history is not fetched."
                  : "GitHub could not be reached, so the release history is not available right now."}
              </p>
            )}
            {view.stale && (
              <p className="releases__notice" role="status">
                GitHub could not be reached just now: this is the last list fetched.
              </p>
            )}
            {!view.unavailable && view.releases.length === 0 && (
              <p className="releases__notice">No release has been published yet.</p>
            )}
            <ol className="releases__list" aria-label="Releases, newest first">
              {view.releases.map((release) => (
                <li key={release.version}>
                  <ReleaseSection release={release} sectionRef={release.current ? currentRef : undefined} />
                </li>
              ))}
            </ol>
            {releasesURL && (
              <p className="releases__all">
                <a href={releasesURL} target="_blank" rel="noopener noreferrer">
                  All releases on GitHub ↗
                </a>
              </p>
            )}
          </>
        )}
      </div>
    </Sheet>
  );
}

function UpgradeBlock({ latest, command }: { latest: string; command: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable: the command can still be selected */
    }
  };
  return (
    <section className="releases__upgrade" aria-label="Upgrade">
      <p className="releases__upgrade-title">{tag(latest)} is available</p>
      <div className="releases__command">
        <code>{command}</code>
        <button
          type="button"
          className="button button--icon"
          aria-label="Copy the upgrade command"
          title={copied ? "Copied" : "Copy"}
          onClick={() => void copy()}
        >
          <Icon name={copied ? "check" : "copy"} size={16} />
        </button>
      </div>
    </section>
  );
}

function ReleaseSection({
  release,
  sectionRef,
}: {
  release: ReleaseEntry;
  sectionRef?: RefObject<HTMLElement | null>;
}) {
  const url = safeURL(release.url);
  const date = new Date(release.published_at);
  const heading = tag(release.version);
  const name = release.name && release.name !== heading && release.name !== release.version ? release.name : "";
  return (
    <section
      ref={sectionRef}
      className={`releases__release${release.current ? " releases__release--current" : ""}`}
      aria-labelledby={`release-${release.version}`}
      aria-current={release.current ? "true" : undefined}
    >
      <header className="releases__release-head">
        <h3 id={`release-${release.version}`} className="releases__version">
          {url ? (
            <a href={url} target="_blank" rel="noopener noreferrer">
              {heading}
            </a>
          ) : (
            heading
          )}
        </h3>
        {release.current && <span className="pill pill--ok">Current</span>}
        {release.prerelease && <span className="pill">Pre-release</span>}
        {!Number.isNaN(date.getTime()) && (
          <time className="releases__date" dateTime={release.published_at}>
            {date.toLocaleDateString("en", { day: "numeric", month: "short", year: "numeric" })}
          </time>
        )}
      </header>
      {name && <p className="releases__name">{name}</p>}
      <Markdown>{withoutTitleLine(release)}</Markdown>
      {release.truncated && (
        <p className="releases__truncated">
          Truncated —{" "}
          {url ? (
            <a href={url} target="_blank" rel="noopener noreferrer">
              read it on GitHub ↗
            </a>
          ) : (
            "read it on GitHub"
          )}
        </p>
      )}
    </section>
  );
}
