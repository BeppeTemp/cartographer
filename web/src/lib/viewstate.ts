/**
 * The URL is the view state.
 *
 * KB, scope, selected concept and the active panel all live in the query
 * string, so browser Back and Forward restore what the user was looking at and
 * a link to a concept is a link someone else can open. Nothing sensitive goes
 * here: a URL is logged by proxies, pasted into chats and kept in history, so
 * the bearer token never touches it.
 */

export type Panel = "atlas" | "observatory" | "artifacts";

export interface ViewState {
  kb: string | null;
  scope: string | null;
  concept: string | null;
  panel: Panel;
  /** The selected artifact, `kind/name`: only on the Artifacts panel. */
  artifact: string | null;
}

export const EMPTY_VIEW: ViewState = { kb: null, scope: null, concept: null, panel: "atlas", artifact: null };

export function readViewState(search: string = window.location.search): ViewState {
  const params = new URLSearchParams(search);
  const param = params.get("panel");
  const panel: Panel = param === "observatory" || param === "artifacts" ? param : "atlas";
  return {
    kb: params.get("kb"),
    scope: params.get("scope"),
    concept: params.get("concept"),
    panel,
    // An artifact without its panel is ignored, not carried to another one.
    artifact: panel === "artifacts" ? params.get("artifact") : null,
  };
}

export function viewStateToSearch(view: ViewState): string {
  const params = new URLSearchParams();
  if (view.kb) params.set("kb", view.kb);
  if (view.scope) params.set("scope", view.scope);
  if (view.concept) params.set("concept", view.concept);
  if (view.panel !== "atlas") params.set("panel", view.panel);
  if (view.panel === "artifacts" && view.artifact) params.set("artifact", view.artifact);
  const query = params.toString();
  return query ? `?${query}` : "";
}

export function sameView(a: ViewState, b: ViewState): boolean {
  return (
    a.kb === b.kb &&
    a.scope === b.scope &&
    a.concept === b.concept &&
    a.panel === b.panel &&
    a.artifact === b.artifact
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
