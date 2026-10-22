import { useEffect, useState, type ReactNode } from "react";
import { fetchArtifact } from "../api/client";
import type { Artifact, ArtifactList } from "../api/types";
import { EmptyKindTile, FindingSummary, splitFrontmatter } from "./Artifacts";
import { Markdown } from "./Markdown";
import { Page, PageHeader, TextSkeleton, TitleBone } from "./Page";
import { ErrorState } from "./States";

/**
 * The KB's instructions, read as what they are: one text, its standing
 * orders, not a tile to open. The page shows the text itself, and above it
 * the clients whose instruction file it is written into.
 */
export function Instructions({
  kb,
  list,
  loading,
  error,
  tabs,
  onRetry,
  onFailure,
}: {
  kb: string;
  list: ArtifactList | null;
  loading: boolean;
  error: unknown;
  tabs?: ReactNode;
  onRetry(): void;
  /** True when the failure was handled (a 401 sends the user to sign in). */
  onFailure(err: unknown): boolean;
}) {
  const listed = (list?.artifacts ?? []).filter((a) => a.kind === "instructions");
  const names = listed.map((a) => a.name).join("\u0000");
  const [read, setRead] = useState<{ key: string; items: Artifact[] } | null>(null);
  const [readError, setReadError] = useState<unknown>(null);

  useEffect(() => {
    if (!names) return;
    const controller = new AbortController();
    setReadError(null);
    Promise.all(names.split("\u0000").map((name) => fetchArtifact(kb, "instructions", name, controller.signal)))
      .then((items) => setRead({ key: `${kb}:${names}`, items }))
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!onFailure(err)) setReadError(err);
      });
    return () => controller.abort();
  }, [kb, names, onFailure]);

  const failure = error ?? readError;
  if (failure) return <ErrorState error={failure} onRetry={onRetry} />;

  const items = read?.key === `${kb}:${names}` ? read.items : null;
  const info = list?.kinds?.find((k) => k.kind === "instructions");
  const clients = [...new Set(listed.flatMap((a) => a.clients.map((c) => c.name || c.id)))];

  if ((loading && !list) || (names && !items)) {
    return (
      <Page label="Instructions" busy className="instructions">
        <PageHeader eyebrow="Instructions" title={<TitleBone label="Loading the instructions" />} />
        {tabs}
        <TextSkeleton label="Loading the instructions" lines={10} />
      </Page>
    );
  }

  return (
    <Page label="Instructions" className="instructions">
      <PageHeader
        eyebrow="Instructions"
        title={listed.length === 0 ? "No instructions yet." : "The KB's standing orders."}
        subtitle={
          listed.length > 0 && (
            <>
              {clients.length === 0
                ? "Kept in the KB, written into no client's instruction file"
                : `Written into the instruction file of ${clients.join(", ")}`}
              {" · "}
              <FindingSummary items={listed} />
            </>
          )
        }
      />
      {tabs}
      {listed.length === 0 && info && (
        <div className="instructions__empty">
          <EmptyKindTile info={info} />
        </div>
      )}
      {items?.map((a) => (
        <article key={a.name} className="instructions__doc" aria-label={`Instructions ${a.name}`}>
          {items.length > 1 && <h2 className="instructions__name">{a.name}</h2>}
          {a.files
            .filter((f) => !f.binary && !f.truncated)
            .map((f) => {
              const text = f.content ?? "";
              return (
                <section key={f.path} className="instructions__file">
                  {a.files.length > 1 && (
                    <p className="artifacts__path">
                      <code>{f.path}</code>
                    </p>
                  )}
                  {f.path.endsWith(".md") ? (
                    <Markdown>{splitFrontmatter(text)?.body ?? text}</Markdown>
                  ) : (
                    <pre className="artifacts__pre">{text}</pre>
                  )}
                </section>
              );
            })}
          {!!a.findings?.length && (
            <ul className="instructions__findings" aria-label="Findings">
              {a.findings.map((f, i) => (
                <li key={`${f.check}-${i}`}>
                  <code>{f.check}</code> {f.message}
                </li>
              ))}
            </ul>
          )}
        </article>
      ))}
    </Page>
  );
}
