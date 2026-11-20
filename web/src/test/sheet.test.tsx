import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Sheet } from "../components/Sheet";

describe("Sheet", () => {
  it("has its own close button by default", () => {
    const onClose = vi.fn();
    render(
      <Sheet side="end" label="Inspector" onClose={onClose}>
        <p>body</p>
      </Sheet>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Close inspector" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("leaves the close button to content that brings its own, and still closes on Escape", () => {
    const onClose = vi.fn();
    render(
      <Sheet side="end" label="Inspector" closeButton={false} onClose={onClose}>
        <button type="button">Close inspector</button>
      </Sheet>,
    );
    expect(screen.getAllByRole("button", { name: "Close inspector" })).toHaveLength(1);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
