import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ContentBoundary } from "../components/ErrorBoundary";

function Boom(): never {
  throw new Error("bad content");
}

describe("ContentBoundary", () => {
  it("a throwing child leaves its siblings mounted and clears when the key changes", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { rerender } = render(
      <div>
        <p>shell</p>
        <ContentBoundary resetKey="a">
          <Boom />
        </ContentBoundary>
      </div>,
    );
    expect(screen.getByText("shell")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("could not be displayed");
    rerender(
      <div>
        <p>shell</p>
        <ContentBoundary resetKey="b">
          <p>fine</p>
        </ContentBoundary>
      </div>,
    );
    expect(screen.getByText("fine")).toBeTruthy();
  });
});
