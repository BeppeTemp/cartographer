import { afterEach, describe, expect, it } from "vitest";
import { formatDay } from "../components/GrowthTimeline";

describe("formatDay", () => {
  const tz = process.env.TZ;
  afterEach(() => {
    if (tz === undefined) delete process.env.TZ;
    else process.env.TZ = tz;
  });

  it("names a UTC day by its UTC date, west of UTC too (the days are grouped in UTC)", () => {
    process.env.TZ = "America/New_York";
    expect(formatDay(Date.UTC(2026, 0, 1))).toBe("Jan 1, 2026");
    expect(formatDay(Date.UTC(2026, 0, 1, 23, 59))).toBe("Jan 1, 2026");
  });
});
