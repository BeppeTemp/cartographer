import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { linkWikiLinks, Markdown, resolveConceptHref } from "../components/Markdown";

describe("resolveConceptHref", () => {
  it.each([
    ["page.md", "area/page"],
    ["../other/x.md", "other/x"],
    ["/x.md", "x"],
    ["sub/index.md", "area/sub"],
    ["page.md#sec", "area/page"],
  ])("%s from area/a is %s", (href, want) => {
    expect(resolveConceptHref(href, "area/a")).toBe(want);
  });
  it.each(["https://example.com/x.md", "//host/x.md", "#frag", "file.pdf", "mailto:a@example.com", "page"])(
    "leaves %s alone",
    (href) => expect(resolveConceptHref(href, "area/a")).toBeNull(),
  );
  it("an expanded concept resolves against the base the server resolved", () => {
    expect(resolveConceptHref("s.md", "map/c", ["map/c/s"])).toBe("map/c/s");
    expect(resolveConceptHref("s.md", "map/c", ["map/s"])).toBe("map/s");
  });
});

describe("links in a concept body", () => {
  it("a relative .md link navigates inside the Atlas and a wiki-link carries the kb", async () => {
    const onNavigate = vi.fn();
    render(
      <Markdown
        conceptId="area/a"
        onNavigate={onNavigate}
        hrefOf={(id) => `?kb=kb-a&concept=${encodeURIComponent(id)}`}
      >
        {"[p](page.md) and [[infra/x]] and [e](https://example.com)"}
      </Markdown>,
    );
    await userEvent.click(screen.getByText("p"));
    expect(onNavigate).toHaveBeenCalledWith("area/page");
    expect(screen.getByText("infra/x").getAttribute("href")).toBe("?kb=kb-a&concept=infra%2Fx");
    expect(screen.getByText(/^e/).getAttribute("href")).toBe("https://example.com");
  });

  it("leaves a wiki-link literal inside a ~~~ fence and a double-backtick span", () => {
    expect(linkWikiLinks("~~~\n[[a/b]]\n~~~\nx")).toBe("~~~\n[[a/b]]\n~~~\nx");
    expect(linkWikiLinks("see ``[[a/b]] ` x`` and [[c/d]]")).toBe(
      "see ``[[a/b]] ` x`` and [c/d](concept-link/c%2Fd)",
    );
  });

  it("no node attribute reaches the DOM", () => {
    const { container } = render(<Markdown conceptId="a/b">{"[x](y.md) [e](https://example.com)"}</Markdown>);
    expect(container.querySelector("[node]")).toBeNull();
  });
});
