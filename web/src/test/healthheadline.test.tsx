import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Health } from "../components/Health";
import type { CheckCatalog, MaintenanceReview } from "../api/types";
import { lint } from "./fixtures";

const checks = { checks: [] } as unknown as CheckCatalog;
const review = { items: [] } as unknown as MaintenanceReview;

function page(extra: { review?: MaintenanceReview | null; checks?: CheckCatalog | null; pending?: boolean }) {
  return (
    <Health
      report={lint}
      status={null}
      summary={null}
      scopeTitle={null}
      loading={false}
      error={null}
      onReveal={() => {}}
      onRetry={() => {}}
      review={extra.review ?? null}
      checks={extra.checks ?? null}
      pending={extra.pending ?? false}
    />
  );
}

describe("Health's headline", () => {
  it("waits while its sources are pending, then shows the switch with it", () => {
    const { rerender } = render(page({ pending: true }));
    expect(screen.getByText(/Reading the KB/)).toBeInTheDocument();
    expect(screen.queryByText(/Cartographer found/)).toBeNull();
    // lint and the checks are in, the review is not: still no number to be corrected later
    rerender(page({ checks, pending: true }));
    expect(screen.queryByText(/Cartographer found/)).toBeNull();
    rerender(page({ checks, review }));
    expect(screen.getByText(/Cartographer found/)).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Health view" })).toBeInTheDocument();
  });

  it("shows the headline when a source failed: it is settled, not pending", () => {
    render(page({ checks }));
    expect(screen.getByText(/Cartographer found/)).toBeInTheDocument();
  });
});
