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
  it("decodes the path, and keeps a malformed escape as written", () => {
    expect(resolveConceptHref("my%20page.md", "area/a")).toBe("area/my page");
    expect(resolveConceptHref("bad%E0%A4%A.md", "area/a")).toBe("area/bad%E0%A4%A");
  });

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

  it("a fence closes on the opener's character and length, not on any run", () => {
    expect(linkWikiLinks("````\n```\n[[a]]\n```\n[[b]]\n````\n[[c]]")).toBe(
      "````\n```\n[[a]]\n```\n[[b]]\n````\n[c](concept-link/c)",
    );
    expect(linkWikiLinks("~~~\n```\n[[a]]\n~~~\n[[b]]")).toBe("~~~\n```\n[[a]]\n~~~\n[b](concept-link/b)");
  });

  it("an inline span does not cross a paragraph break", () => {
    expect(linkWikiLinks("a `x\n\n[[a/b]] and `y`")).toBe("a `x\n\n[a/b](concept-link/a%2Fb) and `y`");
  });

  it("no node attribute reaches the DOM", () => {
    const { container } = render(<Markdown conceptId="a/b">{"[x](y.md) [e](https://example.com)"}</Markdown>);
    expect(container.querySelector("[node]")).toBeNull();
  });
});

describe("a malformed wiki-link target", () => {
  it.each(["%FF", "%C3", "a%C3", "%E0%A4%A"])("%s renders as plain text without throwing", (esc) => {
    const { container } = render(<Markdown conceptId="a/b">{`see [label](concept-link/${esc}) end`}</Markdown>);
    expect(container.textContent).toContain("label");
    expect(container.querySelector("a")).toBeNull();
  });
  it("a valid escape still navigates", () => {
    const { container } = render(<Markdown onNavigate={() => {}}>{"[[a b/c]]"}</Markdown>);
    expect(container.querySelector("a.markdown__wikilink")).not.toBeNull();
  });
});

// AC-cross-kb-links-21
describe("linkWikiLinks cross", () => {
  const cross = [
    { direction: "out" as const, kb: "kb-b", id: "infra/dns", state: "resolved" as const },
    { direction: "out" as const, kb: "kb-b", id: "infra/gone", state: "missing" as const },
    { direction: "out" as const, kb: "kb-b", id: "infra/hidden", state: "unresolved" as const },
    { direction: "out" as const, kb: "kb-b", id: "infra/scope", state: "out_of_scope" as const },
  ];
  it("renders a resolved link as a link to the other KB's page", () => {
    expect(linkWikiLinks("see [[kb:kb-b/infra/dns|the DNS]]", cross)).toBe("see [the DNS](cross-link/kb-b/infra%2Fdns)");
  });
  it.each(["infra/gone", "infra/hidden", "infra/scope", "infra/none"])("%s is plain text with no href", (id) => {
    expect(linkWikiLinks(`[[kb:kb-b/${id}]]`, cross)).toBe(`kb:kb-b/${id}`);
  });
  it("a link with no list, a server qualifier or in code is never a link", () => {
    expect(linkWikiLinks("[[kb:kb-b/infra/dns]]")).toBe("kb:kb-b/infra/dns");
    expect(linkWikiLinks("[[kb:kb-b@srv/infra/dns]]", cross)).toBe("kb:kb-b@srv/infra/dns");
    expect(linkWikiLinks("`[[kb:kb-b/infra/dns]]`", cross)).toBe("`[[kb:kb-b/infra/dns]]`");
  });
  it("an in-KB link keeps its form", () => {
    expect(linkWikiLinks("[[a/b]]", cross)).toBe("[a/b](concept-link/a%2Fb)");
  });
  it("follows a resolved link into the other KB, and draws the rest as text", async () => {
    const open = vi.fn();
    const { container } = render(
      <Markdown conceptId="a/b" cross={cross} onOpenCross={open}>
        {"[[kb:kb-b/infra/dns]] and [[kb:kb-b/infra/hidden]]"}
      </Markdown>,
    );
    expect(container.querySelectorAll("a")).toHaveLength(1);
    await userEvent.click(screen.getByRole("link", { name: "kb:kb-b/infra/dns" }));
    expect(open).toHaveBeenCalledWith("kb-b", "infra/dns");
    expect(container.textContent).toContain("kb:kb-b/infra/hidden");
  });
});
