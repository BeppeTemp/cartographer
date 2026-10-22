/**
 * The URL is the view state.
 *
 * KB, scope, selected concept and the active panel all live in the query
 * string, so browser Back and Forward restore what the user was looking at and
 * a link to a concept is a link someone else can open. Nothing sensitive goes
 * here: a URL is logged by proxies, pasted into chats and kept in history, so
 * the bearer token never touches it.
 */

export type Panel =
  | "atlas"
  | "activity"
  | "retired"
  | "work"
  | "health"
  | "artifacts"
  | "instructions"
  | "templates"
  | "secrets"
  | "settings";

/** The panels of the Artifacts section, one tab each. */
export const ARTIFACT_PANELS: Panel[] = ["artifacts", "instructions", "templates", "secrets"];

/**
 * The tab an artifact of a kind opens in: the KB's instructions and its
 * templates have tabs of their own, every other kind (skill, subagent, hook,
 * MCP server) is the agent kit.
 */
export function artifactPanel(kind: string): "artifacts" | "instructions" | "templates" {
  return kind === "instructions" ? "instructions" : kind === "template" ? "templates" : "artifacts";
}

export interface ViewState {
  kb: string | null;
  scope: string | null;
  concept: string | null;
  panel: Panel;
  /** The selected artifact, `kind/name`: only on the agent kit and Templates panels. */
  artifact: string | null;
  /** Health's own Map filter: only on the Health panel; the rail's scope never reaches it. */
  hmap: string | null;
}

export const EMPTY_VIEW: ViewState = {
  kb: null,
  scope: null,
  concept: null,
  panel: "atlas",
  artifact: null,
  hmap: null,
};

export function readViewState(search: string = window.location.search): ViewState {
  const params = new URLSearchParams(search);
  const param = params.get("panel");
  // Observatory and Maintenance became Health (D338); Services became the
  // Secrets page, grouped by Service, and the pending Sources a Work column, and
  // Paths a part of Settings (D818): their old links still land there rather than on the atlas.
  let panel: Panel =
    param === "maintenance" || param === "observatory"
      ? "health"
      : param === "services"
        ? "secrets"
        : param === "sources"
          ? "work"
          : param === "paths"
            ? "settings"
            : param === "activity" ||
                param === "retired" ||
                param === "work" ||
                param === "health" ||
                param === "artifacts" ||
                param === "instructions" ||
                param === "templates" ||
                param === "secrets" ||
                param === "settings"
              ? param
              : "atlas";
  // An artifact link from before the section had tabs (D1057) opens the tab of its kind.
  const linked = panel === "artifacts" ? params.get("artifact") : null;
  if (linked) {
    const tab = artifactPanel(linked.slice(0, linked.indexOf("/")));
    if (tab !== "artifacts") panel = tab;
  }
  return {
    kb: params.get("kb"),
    scope: params.get("scope"),
    concept: params.get("concept"),
    panel,
    // An artifact without its panel is ignored, not carried to another one.
    artifact: panel === "artifacts" || panel === "templates" ? params.get("artifact") : null,
    hmap: panel === "health" ? params.get("hmap") : null,
  };
}

export function viewStateToSearch(view: ViewState): string {
  const params = new URLSearchParams();
  if (view.kb) params.set("kb", view.kb);
  if (view.scope) params.set("scope", view.scope);
  if (view.concept) params.set("concept", view.concept);
  if (view.panel !== "atlas") params.set("panel", view.panel);
  if ((view.panel === "artifacts" || view.panel === "templates") && view.artifact)
    params.set("artifact", view.artifact);
  if (view.panel === "health" && view.hmap) params.set("hmap", view.hmap);
  const query = params.toString();
  return query ? `?${query}` : "";
}

export function sameView(a: ViewState, b: ViewState): boolean {
  return (
    a.kb === b.kb &&
    a.scope === b.scope &&
    a.concept === b.concept &&
    a.panel === b.panel &&
    a.artifact === b.artifact &&
    a.hmap === b.hmap
  );
}

/**
 * pushView adds a history entry; replaceView rewrites the current one. The
 * distinction matters: selecting a concept is a navigation the user expects
 * Back to undo, while the initial "which KB did we land on" resolution is not.
 */
export function pushView(view: ViewState): void {
  window.history.pushState(null, "", viewStateToSearch(view) || window.location.pathname);
}

export function replaceView(view: ViewState): void {
  window.history.replaceState(null, "", viewStateToSearch(view) || window.location.pathname);
}
