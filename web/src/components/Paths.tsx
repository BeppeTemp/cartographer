import { useEffect, useMemo, useState } from "react";
import { fetchPlaceholders } from "../api/client";
import type { PlaceholderKey, PlaceholderList } from "../api/types";
import { Count, Page, PageHeader, PageSection } from "./Page";
import { EmptyState, ErrorState, Skeleton } from "./States";

/**
 * The Paths panel: the `{{repo:key}}` and `{{path:key}}` placeholders a KB
 * cites, the paths.yaml that declares them, who cites each and which flags
 * need a look. Where a key lives on a machine is not here: resolution is per
 * machine and the server never expands it.
 */
export function Paths({
  kb,
  reloadKey,
  titleOf = () => undefined,
  onOpenConcept,
  onOpenArtifact,
  onRetry,
  onFailure,
}: {
  kb: string;
  /** Changes when the KB moves under the page: the keys are read again. */
  reloadKey: string;
  titleOf?(id: string): string | undefined;
  onOpenConcept?(id: string): void;
  onOpenArtifact?(kind: string, name: string): void;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [list, setList] = useState<PlaceholderList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setList(null);
    setLoading(true);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchPlaceholders(kb, controller.signal)
      .then((data) => {
        setList(data);
        setError(null);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || onFailure(err)) return;
        setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kb, reloadKey]);

  const keys = useMemo(() => list?.keys ?? [], [list]);

  if (loading && !list) return <Skeleton lines={6} label="Loading paths" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!list) return null;
  if (keys.length === 0) {
    return (
      <Page label="Paths" className="ref-page">
        <PageHeader eyebrow="Paths" title="No placeholder keys." />
        <EmptyState
          title={list.registry ? "paths.yaml declares no keys" : "This KB has no paths.yaml"}
          detail="Concepts and artifacts cite a repository or a folder as {{repo:name}} or {{path:name}}, so a page never carries a path from one machine. paths.yaml at the KB root declares each key and what it is; every machine then records where the key lives for it."
        />
        <MachineNote />
      </Page>
    );
  }
  const undeclared = keys.filter((k) => !k.declared).length;
  const unused = keys.filter((k) => k.declared && k.concepts.length === 0 && k.artifacts.length === 0).length;
  return (
    <Page label="Paths" className="ref-page">
      <PageHeader
        eyebrow="Paths"
        title={
          <>
            <Count>{keys.length}</Count> placeholder key{keys.length === 1 ? "" : "s"}
            {undeclared > 0 && (
              <>
                , <Count>{undeclared}</Count> undeclared
              </>
            )}
            {unused > 0 && (
              <>
                , <Count>{unused}</Count> cited nowhere
              </>
            )}
            .
          </>
        }
        subtitle={
          list.registry
            ? "Declared in paths.yaml, cited as {{repo:key}} or {{path:key}} in concepts and artifacts."
            : "This KB has no paths.yaml: the keys below are cited but declared nowhere."
        }
      />
      <PageSection title="Keys" id="paths-keys" count={keys.length}>
        <ul className="ref-list">
          {keys.map((k) => (
            <KeyRow
              key={`${k.kind}:${k.key}`}
              entry={k}
              titleOf={titleOf}
              onOpenConcept={onOpenConcept}
              onOpenArtifact={onOpenArtifact}
            />
          ))}
        </ul>
      </PageSection>
      <MachineNote />
    </Page>
  );
}

function MachineNote() {
  return (
    <p className="page-note">
      Where a key lives is decided per machine and kept by the client, never by the server. Run{" "}
      <code>cartographer paths</code> to list the keys and <code>cartographer paths set &lt;key&gt; &lt;path&gt;</code> to
      record one; <code>cartographer resolve repo:&lt;key&gt;</code> prints the path in use.
    </p>
  );
}

function KeyRow({
  entry: k,
  titleOf,
  onOpenConcept,
  onOpenArtifact,
}: {
  entry: PlaceholderKey;
  titleOf(id: string): string | undefined;
  onOpenConcept?(id: string): void;
  onOpenArtifact?(kind: string, name: string): void;
}) {
  const unused = k.declared && k.concepts.length === 0 && k.artifacts.length === 0;
  return (
    <li className="ref-row">
      <div className="ref-row__head">
        <span className="pill">{k.kind}</span>
        <code className="ref-row__name">{k.key}</code>
        {!k.declared && <span className="pill ref-pill--warn">cited but undeclared</span>}
        {unused && <span className="pill ref-pill--muted">declared, cited nowhere</span>}
        {!!k.conflicts?.length && <span className="pill ref-pill--warn">default differs in another KB</span>}
      </div>
      {k.description && <p className="ref-row__text">{k.description}</p>}
      <dl className="ref-facts">
        {k.default && (
          <div>
            <dt>default</dt>
            <dd>
              <code>{k.default}</code>
            </dd>
          </div>
        )}
        {k.remote && (
          <div>
            <dt>remote</dt>
            <dd>
              <code>{k.remote}</code>
            </dd>
          </div>
        )}
        {!!k.conflicts?.length && (
          <div>
            <dt>conflicts</dt>
            <dd>
              {k.conflicts.map((c) => (
                <span key={c.kb} className="ref-conflict">
                  {c.kb}: <code>{c.default}</code>
                </span>
              ))}
            </dd>
          </div>
        )}
        {k.concepts.length > 0 && (
          <div>
            <dt>cited by</dt>
            <dd>
              {k.concepts.map((id) =>
                onOpenConcept ? (
                  <button key={id} type="button" className="ref-link" onClick={() => onOpenConcept(id)}>
                    {titleOf(id) ?? id}
                  </button>
                ) : (
                  <span key={id}>{titleOf(id) ?? id}</span>
                ),
              )}
            </dd>
          </div>
        )}
        {k.artifacts.length > 0 && (
          <div>
            <dt>artifacts</dt>
            <dd>
              {k.artifacts.map((a) => {
                const [kind, ...rest] = a.split("/");
                const name = rest.join("/");
                return onOpenArtifact ? (
                  <button key={a} type="button" className="ref-link" onClick={() => onOpenArtifact(kind!, name)}>
                    {a}
                  </button>
                ) : (
                  <span key={a}>{a}</span>
                );
              })}
            </dd>
          </div>
        )}
      </dl>
    </li>
  );
}
