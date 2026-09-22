import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  clearToken,
  fetchConcept,
  fetchGraph,
  fetchKBs,
  fetchLint,
  fetchOverview,
  restoreToken,
  setToken,
} from "./api/client";
import type { Concept, GraphSnapshot, KBSummary, LintReport, Overview } from "./api/types";
import { AuthPrompt } from "./components/AuthPrompt";
import { CommandPalette } from "./components/CommandPalette";
import { GraphCanvas } from "./components/GraphCanvas";
import { Inspector } from "./components/Inspector";
import { LeftRail } from "./components/LeftRail";
import { Legend } from "./components/Legend";
import { Sheet, useMediaQuery } from "./components/Sheet";
import { NodeList } from "./components/NodeList";
import { Observatory } from "./components/Observatory";
import { EmptyState, ErrorState, Skeleton } from "./components/States";
import { TopBar } from "./components/TopBar";
import { applyTheme, readTheme, type Theme } from "./lib/theme";
import { communitySlot, detectCommunities, type Communities } from "./lib/communities";
import {
  collectionHue,
  readColorBy,
  slotVar,
  writeColorBy,
  type ColorBy,
} from "./lib/palette";
import { pushView, readViewState, replaceView, type ViewState } from "./lib/viewstate";
import { readPanel, writePanel } from "./lib/panels";

type Phase = "booting" | "auth" | "ready";
type SheetName = "nav" | "inspector" | null;

const NO_COMMUNITIES: Communities = { rankOf: new Map(), list: [] };

/** Below this width the rail and the inspector become modal sheets. Kept in
 *  step with the max-width: 1023px media queries in the stylesheets. */
const NARROW_QUERY = "(max-width: 1023px)";
/** The inspector card's width plus its margin (--inspector-width + gutter):
 *  the strip of canvas a selection must not be centred under. */
const INSPECTOR_OCCLUSION = 420 + 32;

export function App() {
  const [phase, setPhase] = useState<Phase>("booting");
  const [authRejected, setAuthRejected] = useState(false);
  const [theme, setTheme] = useState<Theme>(readTheme);
  const [view, setView] = useState<ViewState>(() => readViewState());

  const [kbs, setKBs] = useState<KBSummary[]>([]);
  const [bootError, setBootError] = useState<unknown>(null);

  const [overview, setOverview] = useState<Overview | null>(null);
  const [snapshot, setSnapshot] = useState<GraphSnapshot | null>(null);
  const [graphError, setGraphError] = useState<unknown>(null);
  const [graphLoading, setGraphLoading] = useState(false);

  const [concept, setConcept] = useState<Concept | null>(null);
  const [conceptError, setConceptError] = useState<unknown>(null);
  const [conceptLoading, setConceptLoading] = useState(false);

  const [lint, setLint] = useState<LintReport | null>(null);
  const [lintError, setLintError] = useState<unknown>(null);
  const [lintLoading, setLintLoading] = useState(false);
  const [severityMin, setSeverityMin] = useState("info");

  const [typeFilter, setTypeFilter] = useState<Set<string>>(new Set());
  const [statusFilter, setStatusFilter] = useState<Set<string>>(new Set());
  // The graph is the page: navigation and the node list start folded away
  // and open on demand, and the choice is remembered (lib/panels).
  const [railCollapsed, setRailCollapsed] = useState(() => readPanel("rail", true));
  const [listOpen, setListOpen] = useState(() => readPanel("list", false));
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [preview, setPreview] = useState<string | null>(null);
  const [notice, setNotice] = useState<string>("");
  const [offline, setOffline] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [colorBy, setColorBy] = useState<ColorBy>(readColorBy);
  const narrow = useMediaQuery(NARROW_QUERY);
  const [sheet, setSheet] = useState<SheetName>(null);

  useEffect(() => applyTheme(theme), [theme]);
  useEffect(() => writeColorBy(colorBy), [colorBy]);
  useEffect(() => writePanel("rail", railCollapsed), [railCollapsed]);
  useEffect(() => writePanel("list", listOpen), [listOpen]);
  // Leaving the narrow layout closes any sheet: on a wide screen the panels
  // are simply there, and a leftover modal would trap focus over them.
  useEffect(() => {
    if (!narrow) setSheet(null);
  }, [narrow]);

  /** A 401 anywhere returns to the prompt while keeping the view state: the
   *  user comes back to the concept they were reading, not to a blank atlas. */
  const handleFailure = useCallback((err: unknown) => {
    if (err instanceof ApiError && err.isUnauthorized) {
      clearToken();
      setAuthRejected(true);
      setPhase("auth");
      return true;
    }
    if (err instanceof ApiError && err.isOffline) setOffline(true);
    return false;
  }, []);

  const loadKBs = useCallback(async () => {
    try {
      const { kbs: list } = await fetchKBs();
      setOffline(false);
      setKBs(list);
      setBootError(null);
      setPhase("ready");
      setAuthRejected(false);
      return list;
    } catch (err) {
      if (handleFailure(err)) return null;
      setBootError(err);
      setPhase("ready");
      return null;
    }
  }, [handleFailure]);

  // Boot: try the restored tab token, then an unauthenticated call. A server
  // with auth off answers the second one, which is the local-mode experience.
  useEffect(() => {
    restoreToken();
    void loadKBs();
  }, [loadKBs, reloadKey]);

  // Resolve the KB once the list is known, without adding a history entry:
  // "which KB did we land on" is not a navigation the user made.
  useEffect(() => {
    if (kbs.length === 0) return;
    if (view.kb && kbs.some((kb) => kb.name === view.kb)) return;
    const next = { ...view, kb: kbs[0]!.name, scope: null, concept: null };
    setView(next);
    replaceView(next);
  }, [kbs, view]);

  // Back/Forward restores everything the URL carries.
  useEffect(() => {
    const onPop = () => setView(readViewState());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback(
    (next: Partial<ViewState>) => {
      setView((current) => {
        const merged = { ...current, ...next };
        pushView(merged);
        return merged;
      });
    },
    [],
  );

  // --- Data loading. Every request is abortable, so a fast click sequence
  // cannot let a stale response overwrite a newer one. ---

  const activeKB = view.kb;

  useEffect(() => {
    if (!activeKB || phase !== "ready") return;
    const controller = new AbortController();
    fetchOverview(activeKB, controller.signal)
      .then((data) => {
        setOverview(data);
        setOffline(false);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!handleFailure(err)) setOverview(null);
      });
    return () => controller.abort();
  }, [activeKB, phase, handleFailure, reloadKey]);

  useEffect(() => {
    if (!activeKB || phase !== "ready") return;
    const controller = new AbortController();
    setGraphLoading(true);
    fetchGraph(activeKB, view.scope, controller.signal)
      .then((data) => {
        setSnapshot(data);
        setGraphError(null);
        setOffline(false);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!handleFailure(err)) setGraphError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setGraphLoading(false);
      });
    return () => controller.abort();
  }, [activeKB, view.scope, phase, handleFailure, reloadKey]);

  useEffect(() => {
    if (!activeKB || !view.concept || phase !== "ready") {
      setConcept(null);
      setConceptError(null);
      return;
    }
    const controller = new AbortController();
    setConceptLoading(true);
    fetchConcept(activeKB, view.concept, controller.signal)
      .then((data) => {
        setConcept(data);
        setConceptError(null);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!handleFailure(err)) {
          setConcept(null);
          setConceptError(err);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setConceptLoading(false);
      });
    return () => controller.abort();
  }, [activeKB, view.concept, phase, handleFailure]);

  useEffect(() => {
    if (!activeKB || phase !== "ready") return;
    const controller = new AbortController();
    setLintLoading(true);
    fetchLint(activeKB, severityMin, controller.signal)
      .then((data) => {
        setLint(data);
        setLintError(null);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!handleFailure(err)) setLintError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLintLoading(false);
      });
    return () => controller.abort();
  }, [activeKB, severityMin, phase, handleFailure, reloadKey]);

  // --- Derived view ---

  // Worst severity per concept: the graph tints a node by it, so "error"
  // must win over "warning" rather than whichever finding came last.
  const severityByConcept = useMemo(() => {
    const rank: Record<string, number> = { info: 0, warning: 1, error: 2 };
    const worst = new Map<string, string>();
    for (const finding of lint?.findings ?? []) {
      if (!finding.concept) continue;
      const current = worst.get(finding.concept);
      if (!current || (rank[finding.severity] ?? -1) > (rank[current] ?? -1)) {
        worst.set(finding.concept, finding.severity);
      }
    }
    return worst;
  }, [lint]);

  const findingsByConcept = useMemo(() => {
    const map = new Map<string, LintReport["findings"]>();
    for (const finding of lint?.findings ?? []) {
      if (!finding.concept) continue;
      const list = map.get(finding.concept) ?? [];
      list.push(finding);
      map.set(finding.concept, list);
    }
    return map;
  }, [lint]);

  const hiddenIds = useMemo(() => {
    const hidden = new Set<string>();
    if (!snapshot || (typeFilter.size === 0 && statusFilter.size === 0)) return hidden;
    for (const node of snapshot.nodes) {
      const typeOK = typeFilter.size === 0 || (node.type ? typeFilter.has(node.type) : false);
      const statusOK =
        statusFilter.size === 0 || (node.status ? statusFilter.has(node.status) : false);
      if (!typeOK || !statusOK) hidden.add(node.id);
    }
    return hidden;
  }, [snapshot, typeFilter, statusFilter]);

  const visibleNodes = useMemo(
    () => (snapshot?.nodes ?? []).filter((node) => !hiddenIds.has(node.id)),
    [snapshot, hiddenIds],
  );

  // Computed on the whole snapshot, not on the filtered view: a filter must
  // not recolour what remains, or toggling a chip reshuffles every colour.
  const communities = useMemo(
    () => (snapshot ? detectCommunities(snapshot) : NO_COMMUNITIES),
    [snapshot],
  );

  const swatchFor = useCallback(
    (id: string, collection: string | undefined) =>
      slotVar(
        colorBy === "community" ? communitySlot(communities, id) : collectionHue(collection ?? ""),
      ),
    [colorBy, communities],
  );

  // Ctrl/Cmd+K anywhere, Escape to clear the selection.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen(true);
      } else if (event.key === "Escape" && !paletteOpen) {
        setView((current) => {
          if (!current.concept) return current;
          const next = { ...current, concept: null };
          pushView(next);
          return next;
        });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [paletteOpen]);

  const noticeTimer = useRef<number | undefined>(undefined);
  const announce = useCallback((message: string) => {
    setNotice(message);
    window.clearTimeout(noticeTimer.current);
    noticeTimer.current = window.setTimeout(() => setNotice(""), 6000);
  }, []);

  const selectConcept = useCallback(
    (id: string | null) => {
      navigate({ concept: id });
      // On a narrow screen a selection is only visible in its sheet.
      if (id && narrow) setSheet("inspector");
    },
    [navigate, narrow],
  );

  /** Closing the inspector hands focus back to the concept's row in the node
   *  list, so a keyboard user continues where they were instead of starting
   *  over from the top of the page. */
  const closeInspector = useCallback(() => {
    const id = view.concept;
    selectConcept(null);
    setSheet(null);
    window.requestAnimationFrame(() => {
      const row = id
        ? document.querySelector<HTMLElement>(`[data-concept-id="${CSS.escape(id)}"]`)
        : null;
      (row ?? document.getElementById("main"))?.focus();
    });
  }, [selectConcept, view.concept]);

  const expandConcept = useCallback(
    (id: string) => {
      const collection = snapshot?.nodes.find((node) => node.id === id)?.collection;
      navigate({ concept: id, scope: collection ?? view.scope });
    },
    [navigate, snapshot, view.scope],
  );

  if (phase === "booting") {
    return <Skeleton lines={4} label="Connecting to Cartographer" />;
  }

  if (phase === "auth") {
    return (
      <AuthPrompt
        rejected={authRejected}
        onSubmit={(token, remember) => {
          setToken(token, remember);
          setAuthRejected(false);
          setPhase("booting");
          setReloadKey((k) => k + 1);
        }}
      />
    );
  }

  // The rail, node list and inspector are rendered in place on a wide screen
  // and inside modal sheets on a narrow one; built once here so the two
  // layouts cannot drift apart.
  const rail = (inSheet: boolean) => (
    <LeftRail
      overview={overview}
      snapshot={snapshot}
      scope={view.scope}
      panel={view.panel}
      collapsed={inSheet ? false : railCollapsed}
      collapsible={!inSheet}
      typeFilter={typeFilter}
      statusFilter={statusFilter}
      onScope={(scope) => {
        navigate({ scope, concept: null });
        if (inSheet) setSheet(null);
      }}
      onPanel={(panel) => {
        navigate({ panel });
        if (inSheet) setSheet(null);
      }}
      onToggleCollapsed={() => setRailCollapsed((c) => !c)}
      onToggleType={(value) => setTypeFilter(toggle(typeFilter, value))}
      onToggleStatus={(value) => setStatusFilter(toggle(statusFilter, value))}
      onClearFilters={() => {
        setTypeFilter(new Set());
        setStatusFilter(new Set());
      }}
    />
  );

  const nodeList = (
    <NodeList
      nodes={visibleNodes}
      selected={view.concept}
      onSelect={selectConcept}
      swatchFor={swatchFor}
    />
  );

  const inspector = (
    <Inspector
      conceptId={view.concept}
      concept={concept}
      error={conceptError}
      loading={conceptLoading}
      findings={view.concept ? (findingsByConcept.get(view.concept) ?? []) : []}
      onNavigate={selectConcept}
      onPreview={setPreview}
      onClose={closeInspector}
    />
  );

  const bodyClass = [
    "shell__body",
    railCollapsed ? "shell__body--rail-collapsed" : "",
    narrow ? "shell__body--narrow" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <TopBar
        kbs={kbs}
        activeKB={activeKB}
        theme={theme}
        offline={offline}
        narrow={narrow}
        hasSelection={view.panel === "atlas" && view.concept !== null}
        onKBChange={(name) => navigate({ kb: name, scope: null, concept: null })}
        onThemeChange={setTheme}
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenNav={() => setSheet("nav")}
        onOpenInspector={() => setSheet("inspector")}
      />

      <div className={bodyClass}>
        {!narrow && rail(false)}

        <main id="main" className="main" tabIndex={-1}>
          <p className="sr-only" role="status" aria-live="polite">
            {notice}
          </p>
          {bootError ? (
            <ErrorState error={bootError} onRetry={() => setReloadKey((k) => k + 1)} />
          ) : kbs.length === 0 ? (
            <EmptyState
              title="No Knowledge Base is visible"
              detail="This server has no KB mounted that your token can read."
            />
          ) : view.panel === "observatory" ? (
            <Observatory
              report={lint}
              loading={lintLoading}
              error={lintError}
              severityMin={severityMin}
              onSeverityChange={setSeverityMin}
              onRetry={() => setReloadKey((k) => k + 1)}
              onReveal={(conceptId, message) => {
                if (conceptId) navigate({ panel: "atlas", concept: conceptId });
                else announce(message);
              }}
            />
          ) : graphLoading && !snapshot ? (
            <Skeleton lines={4} label="Loading the graph" />
          ) : graphError ? (
            <ErrorState error={graphError} onRetry={() => setReloadKey((k) => k + 1)} />
          ) : !snapshot || snapshot.nodes.length === 0 ? (
            <EmptyState
              title={view.scope ? `${view.scope} is empty` : "This KB has no concepts yet"}
              detail={
                view.scope
                  ? "Nothing in this Map is visible to you."
                  : "Once concepts are written, they appear here as a graph."
              }
            />
          ) : (
            <>
              <GraphCanvas
                kb={activeKB!}
                scope={view.scope}
                snapshot={snapshot}
                communities={communities}
                colorBy={colorBy}
                selected={view.concept}
                highlighted={preview}
                hiddenIds={hiddenIds}
                severityByConcept={severityByConcept}
                themeKey={theme}
                onSelect={selectConcept}
                onExpand={expandConcept}
                occludedRight={!narrow && view.concept ? INSPECTOR_OCCLUSION : 0}
              >
                <Legend
                  snapshot={snapshot}
                  communities={communities}
                  colorBy={colorBy}
                  onColorBy={setColorBy}
                />
              </GraphCanvas>
              {!narrow && (
                <div className="graph-toolbar">
                  <button
                    type="button"
                    className="button graph-toolbar__toggle"
                    aria-pressed={listOpen}
                    aria-controls="concept-list"
                    onClick={() => setListOpen((open) => !open)}
                  >
                    <span aria-hidden="true">&#9776;</span>
                    Concepts
                    <span className="graph-toolbar__count">{visibleNodes.length}</span>
                  </button>
                </div>
              )}
              {!narrow && listOpen && <div id="concept-list" className="overlay overlay--list">{nodeList}</div>}
            </>
          )}
        </main>

        {view.panel === "atlas" && !narrow && view.concept && (
          <div className="overlay overlay--inspector">{inspector}</div>
        )}
      </div>

      {narrow && sheet === "nav" && (
        <Sheet side="start" label="Navigation" onClose={() => setSheet(null)}>
          {rail(true)}
          {snapshot && view.panel === "atlas" && nodeList}
        </Sheet>
      )}
      {narrow && sheet === "inspector" && view.panel === "atlas" && (
        <Sheet side="end" label="Inspector" onClose={() => setSheet(null)}>
          {inspector}
        </Sheet>
      )}

      <CommandPalette
        open={paletteOpen}
        nodes={visibleNodes}
        onClose={() => setPaletteOpen(false)}
        onSelect={selectConcept}
      />
    </div>
  );
}

function toggle(set: Set<string>, value: string): Set<string> {
  const next = new Set(set);
  if (next.has(value)) next.delete(value);
  else next.add(value);
  return next;
}
