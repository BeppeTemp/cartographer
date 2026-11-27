import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConceptDiff } from "../components/ConceptDiff";
import { json, stubApi } from "./fixtures";

afterEach(() => vi.unstubAllGlobals());

const lines = (n: number, mark = "") => Array.from({ length: n }, (_, i) => `line ${i + 1}${i === 9 ? mark : ""}`).join("\n") + "\n";

const answer = (id: string) => ({
  id,
  path: `kb/${id}/index.md`,
  commits: 2,
  base: { sha: "aaaaaaa1111", at: "2026-01-01T00:00:00Z" },
  head: { sha: "bbbbbbb2222", at: "2026-01-02T00:00:00Z" },
  files: [
    { path: `kb/${id}/index.md`, status: "modified", before: lines(30), after: lines(30, " changed") + "added tail\n" },
    { path: `kb/${id}/logo.png`, status: "modified", before: "", after: "", binary: true },
    { path: `kb/${id}/old.md`, status: "deleted", before: "gone\n", after: "" },
  ],
});

function renderDiff(id: string, onOpen = vi.fn()) {
  stubApi({ "/diff": () => json(answer(id)) });
  render(<ConceptDiff kb="kb-a" id={id} from="2026-01-01T00:00:00Z" onOpen={onOpen} />);
  return onOpen;
}

describe("ConceptDiff", () => {
  it("lists its files folded, each with its status and +/- counts, and a binary one as such", async () => {
    renderDiff("notes/fold");
    const region = await screen.findByRole("region", { name: "What changed in notes/fold" });
    expect(await within(region).findByText("3 files changed")).toBeInTheDocument();
    expect(region).toHaveTextContent("aaaaaaa → bbbbbbb · 2 commits");
    const heads = within(region).getAllByRole("button", { expanded: false });
    expect(heads.map((h) => h.getAttribute("aria-expanded"))).toEqual(["false", "false", "false"]);
    // Paths are shown relative to the concept's folder.
    expect(heads[0]).toHaveTextContent("index.md");
    expect(heads[0]).toHaveTextContent("modified");
    expect(heads[0]).toHaveTextContent("+2");
    expect(heads[0]).toHaveTextContent("−1");
    expect(heads[1]).toHaveTextContent("binary");
    expect(heads[2]).toHaveTextContent("deleted");
    expect(region.querySelector(".concept-diff__body")).toBeNull();
  });

  it("opens a file's hunks: the changed lines in context, the rest folded", async () => {
    const user = userEvent.setup();
    renderDiff("notes/hunks");
    const region = await screen.findByRole("region", { name: "What changed in notes/hunks" });
    const head = (await within(region).findAllByRole("button", { expanded: false }))[0]!;
    await user.click(head);
    expect(head).toHaveAttribute("aria-expanded", "true");
    const body = region.querySelector(".concept-diff__body")!;
    expect(body.querySelector('[data-kind="add"]')).toHaveTextContent("line 10 changed");
    expect(body.querySelector('[data-kind="del"]')).toHaveTextContent("line 10");
    expect(body).toHaveTextContent("unchanged lines");
    expect(body).not.toHaveTextContent("line 1\n");
    await user.click(head);
    expect(region.querySelector(".concept-diff__body")).toBeNull();
  });

  it("shows the whole file with the eye, and back to the changed lines", async () => {
    const user = userEvent.setup();
    renderDiff("notes/whole");
    const region = await screen.findByRole("region", { name: "What changed in notes/whole" });
    const eye = await within(region).findByRole("button", { name: "Whole file index.md" });
    await user.click(eye);
    const body = region.querySelector(".concept-diff__body")!;
    // Every line of the end of the span, none folded.
    expect(body.querySelectorAll(".concept-diff__line")).toHaveLength(31);
    expect(body).not.toHaveTextContent("unchanged lines");
    const back = within(region).getByRole("button", { name: "Changed lines of index.md" });
    expect(back).toHaveAttribute("aria-pressed", "true");
    await user.click(back);
    expect(region.querySelector(".concept-diff__body")).toHaveTextContent("unchanged lines");
  });

  it("disables what does not apply: the eye on a binary or a deleted file", async () => {
    renderDiff("notes/disabled");
    const region = await screen.findByRole("region", { name: "What changed in notes/disabled" });
    expect(await within(region).findByRole("button", { name: "Whole file index.md" })).toBeEnabled();
    expect(within(region).getByRole("button", { name: "Whole file logo.png" })).toBeDisabled();
    expect(within(region).getByRole("button", { name: "Whole file old.md" })).toBeDisabled();
  });

  it("opens the page from a file, except the page's own deletion", async () => {
    const user = userEvent.setup();
    const onOpen = renderDiff("notes/page");
    const region = await screen.findByRole("region", { name: "What changed in notes/page" });
    const open = await within(region).findAllByRole("button", { name: "Open the page" });
    expect(open).toHaveLength(3);
    await user.click(open[0]!);
    expect(onOpen).toHaveBeenCalledWith("notes/page");

    stubApi({
      "/diff": () => json({ id: "notes/gone", path: "kb/notes/gone.md", commits: 1, base: { sha: "ccccccc1", at: "" }, files: [{ path: "kb/notes/gone.md", status: "deleted", before: "x\n", after: "" }] }),
    });
    render(<ConceptDiff kb="kb-a" id="notes/gone" from="2026-01-01T00:00:00Z" onOpen={onOpen} />);
    const gone = await screen.findByRole("region", { name: "What changed in notes/gone" });
    expect(await within(gone).findByRole("button", { name: "Open the page" })).toBeDisabled();
    expect(gone).toHaveTextContent("→ deleted");
  });

  it("says so when the files did not change, and when the read fails", async () => {
    stubApi({ "/diff": () => json({ id: "notes/same", path: "kb/notes/same.md", commits: 0, files: [] }) });
    const { unmount } = render(<ConceptDiff kb="kb-a" id="notes/same" from="2026-01-01T00:00:00Z" />);
    expect(await screen.findByText("No change to the files in this span.")).toBeInTheDocument();
    unmount();
    stubApi({ "/diff": () => json({ error: { code: "internal", message: "boom" } }, 500) });
    render(<ConceptDiff kb="kb-a" id="notes/broken" from="2026-01-01T00:00:00Z" />);
    expect(await screen.findByText("Could not read the change")).toBeInTheDocument();
  });

  it("says the history was cut when the diff is truncated and has no base", async () => {
    stubApi({ "/diff": () => json({ ...answer("notes/long"), base: null, truncated: true }) });
    render(<ConceptDiff kb="kb-a" id="notes/long" from="2026-01-01T00:00:00Z" onOpen={vi.fn()} />);
    expect(await screen.findByText(/The history is longer than the diff reads/)).toBeInTheDocument();
    expect(screen.queryByText(/Long change/)).not.toBeInTheDocument();
  });

  it("says a file is too large to diff, and still offers the whole file", async () => {
    const user = userEvent.setup();
    const many = (p: string) => Array.from({ length: 700 }, (_, i) => `${p}${i}`).join("\n") + "\n";
    stubApi({
      "/diff": () =>
        json({ id: "notes/huge", path: "kb/notes/huge.md", commits: 1, files: [{ path: "kb/notes/huge.md", status: "modified", before: many("a"), after: many("b") }] }),
    });
    render(<ConceptDiff kb="kb-a" id="notes/huge" from="2026-01-01T00:00:00Z" />);
    const region = await screen.findByRole("region", { name: "What changed in notes/huge" });
    const head = (await within(region).findAllByRole("button", { expanded: false }))[0]!;
    expect(head).toHaveTextContent("too large to diff");
    await user.click(head);
    expect(region).toHaveTextContent("Too large to compare line by line");
  });
});
