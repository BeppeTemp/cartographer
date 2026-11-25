import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Concept } from "../api/types";
import { Inspector } from "../components/Inspector";
import { stubApi } from "./fixtures";

const concept: Concept = {
  id: "infra/a",
  title: "Alpha",
  collection: "infra",
  frontmatter: {},
  body: "A service.",
  body_bytes: 10,
  outline: [],
  content_hash: "abc123",
  outbound: [],
  inbound: [],
  broken: [],
};

function mount(loading: boolean, loaded: Concept | null) {
  vi.stubGlobal("fetch", stubApi({}));
  return render(
    <Inspector
      kb="kb-a"
      conceptId="infra/a"
      concept={loaded}
      error={null}
      loading={loading}
      findings={[]}
      onNavigate={() => {}}
      onPreview={() => {}}
      onClose={() => {}}
    />,
  );
}

// A tab naming a panel that is not in the DOM is an axe aria-valid-attr-value
// violation: the e2e a11y scan caught the window while the concept loads (#2361).
describe("the Inspector tabs' aria-controls", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("names no panel while the concept loads", () => {
    const { container } = mount(true, null);
    for (const tab of container.querySelectorAll('[role="tab"]')) {
      expect(tab.getAttribute("aria-controls")).toBeNull();
    }
  });

  it("names a panel that exists once the concept is loaded", () => {
    const { container } = mount(false, concept);
    const tabs = container.querySelectorAll('[role="tab"]');
    expect(tabs.length).toBe(4);
    for (const tab of tabs) {
      const target = tab.getAttribute("aria-controls");
      expect(target).not.toBeNull();
      expect(container.querySelector(`#${target}`)).not.toBeNull();
    }
  });
});
