import { useEffect, useState, type ReactNode } from "react";
import { fetchExtensions, fetchViewSource } from "../api/client";
import type { ExtensionView as View, Extensions } from "../api/types";
import { shape, viewPath } from "../lib/extension";
import { KeyValue, List, Stats, Table } from "./extension/Renderers";
import { Page, PageHeader, PageSection, LoadingRegion, Bone } from "./Page";
import { EmptyState, ErrorState } from "./States";

const NONE: Extensions = { views: [], apps: [], plugins: [] };

// Read after boot and when the live revision moves. A failed read keeps the last good list (a 404 is already "none"): the next beat asks again.
// `loaded` tells "not asked yet" from "none", so a link to a view is not dropped before the answer.
export function useExtensions(live: number, enabled: boolean): { extensions: Extensions; loaded: boolean } {
  const [state, setState] = useState<{ extensions: Extensions; loaded: boolean }>({ extensions: NONE, loaded: false });
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    fetchExtensions(controller.signal)
      .then((extensions) => setState({ extensions, loaded: true }))
      .catch(() => undefined);
    return () => controller.abort();
  }, [live, enabled]);
  return state;
}

type Load = { state: "loading" } | { state: "error"; error: unknown } | { state: "ready"; answer: unknown };

/** One plugin view for the picked KB: a GET of its declared route, redrawn when the revision moves. The shell keys it by view and KB, so another KB starts empty. */
export function ExtensionView({ view, kb, live, onOpen }: { view: View; kb: string | null; live: number; onOpen(id: string): void }) {
  const [current, setCurrent] = useState<Load>({ state: "loading" });
  const path = viewPath(view, kb);
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    fetchViewSource(path, controller.signal)
      .then((answer) => setCurrent({ state: "ready", answer }))
      .catch((error: unknown) => !controller.signal.aborted && setCurrent({ state: "error", error }));
    return () => controller.abort();
  }, [path, live]);
  const shell = (body: ReactNode, busy?: boolean) => (
    <Page label={view.title} busy={busy}>
      <PageHeader eyebrow={`Supplied by plugin ${view.plugin}`} title={view.title} subtitle={view.description || undefined} />
      {body}
    </Page>
  );
  if (!path) return shell(<EmptyState title="Pick a Knowledge Base" detail="This view reads the KB you pick in the top bar." />);
  if (current.state === "loading")
    return shell(
      <LoadingRegion label={`Loading ${view.title}`}>
        <Bone w="60%" h={14} />
      </LoadingRegion>,
      true,
    );
  if (current.state === "error") return shell(<ErrorState error={current.error} />);
  const data = shape(view, current.answer);
  const props = { columns: view.columns, label: view.title, onOpen };
  return shell(
      <PageSection title="Data" id={`ext-${view.plugin}-${view.name}`}>
        {!data ? (
          <EmptyState title="Unexpected shape" detail="The route answered, but not with the data this view declares." />
        ) : data.kind === "rows" ? (
          data.rows.length === 0 ? (
            <EmptyState title="Nothing to show" />
          ) : (
            <>
              {view.renderer === "table" ? <Table data={data.rows} {...props} /> : <List data={data.rows} {...props} />}
              {data.more > 0 && <p className="page-note">{data.more} more</p>}
            </>
          )
        ) : view.renderer === "stats" ? (
          <Stats data={data.object} {...props} />
        ) : (
          <KeyValue data={data.object} {...props} />
        )}
      </PageSection>,
  );
}
