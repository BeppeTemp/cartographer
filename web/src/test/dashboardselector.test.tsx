import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { act, Profiler } from "react";
import { Work } from "../components/Work";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

const work = { total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] };
const list = { dashboards: [{ name: "week", title: "This week", widgets: 2, hash: "h1" }, { name: "blocked" }] };
const resolved = (name: string) => ({ name, hash: "h", widgets: [{ id: "w1", type: "stat", total: 3 }] });

// AC-kbdash-17: the selector lists the KB's dashboards plus the default Work
// board, `?dashboard=` survives a reload and resets on a KB change.
describe("the dashboard selector of the Work page", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=work");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  const api = () =>
    stubApi({
      "/dashboards/": () => json(resolved("week")),
      "/dashboards": () => json(list),
      "/work": () => json(work),
    });

  it("offers the Work board first, then each dashboard, names only for a narrowed caller", async () => {
    api();
    const user = userEvent.setup();
    render(<App />);
    const select = await screen.findByRole("combobox", { name: /Dashboard/ });
    await waitFor(() => expect(within(select).getAllByRole("option")).toHaveLength(3));
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["Work board", "This week", "blocked"]);
    await user.selectOptions(select, "week");
    await waitFor(() => expect(window.location.search).toContain("dashboard=week"));
    expect(await screen.findByText(/drawn in a later release/)).toBeInTheDocument();
    await user.selectOptions(select, "");
    expect(window.location.search).not.toContain("dashboard=");
  });

  it("keeps the choice across a reload and resets it on a KB change", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=work&dashboard=week");
    const m = api();
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return json({ kbs: [{ name: "kb-a", status: "normal", ready: true }, { name: "kb-b", status: "normal", ready: true }] });
      if (url.includes("/kbs/kb-b/dashboards")) return json({ dashboards: [] });
      return route(url);
    });
    render(<App />);
    expect(await screen.findByRole("heading", { level: 1, name: "This week" })).toBeInTheDocument();
    expect(window.location.search).toContain("dashboard=week");

    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-b&panel=work");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(screen.queryByRole("combobox", { name: /Dashboard/ })).not.toBeInTheDocument());
    expect(screen.queryByRole("heading", { level: 1, name: "This week" })).not.toBeInTheDocument();
  });

  it("never shows the previous KB's dashboard names under the new KB, not even for one render", async () => {
    const m = api();
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (url.includes("/kbs/kb-b/dashboards")) {
        await new Promise((r) => setTimeout(r, 100));
        return json({ dashboards: [] });
      }
      return route(url);
    });
    // The Profiler reads the DOM of every commit, before passive effects run:
    // the first commit under kb-b is the render a reset in an effect would leak in.
    const commits: boolean[] = [];
    const tree = (kb: string) => (
      <Profiler id="work" onRender={() => commits.push(document.querySelector("option[value='week']") !== null)}>
        <Work kb={kb} dashboard={null} onDashboard={() => undefined} onOpen={() => undefined} />
      </Profiler>
    );
    const { rerender } = render(tree("kb-a"));
    await screen.findByRole("option", { name: "This week" });
    commits.length = 0;
    rerender(tree("kb-b"));
    await new Promise((r) => setTimeout(r, 200));
    expect(commits.length).toBeGreaterThan(0);
    expect(commits[0]).toBe(false);
    expect(commits.every((c) => !c)).toBe(true);
  });

  it("names dashboard_patch when a dashboard has no widget", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=work&dashboard=new");
    stubApi({
      "/dashboards/": () => json({ name: "new", hash: "", widgets: [] }),
      "/dashboards": () => json({ dashboards: [] }),
      "/work": () => json(work),
    });
    render(<App />);
    expect(await screen.findByText(/dashboard_patch/)).toBeInTheDocument();
  });
});
