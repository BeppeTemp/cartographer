import { searchIndex, staticKey } from "./static";

describe("staticKey", () => {
  it("matches the names demo/export.mjs writes", () => {
    // The exporter has its own copy of this function: these literals pin both.
    expect(staticKey("/kbs")).toBe("~2Fkbs");
    expect(staticKey("/kbs/astronomy/concept?id=stars%2Fsirius")).toBe(
      "~2Fkbs~2Fastronomy~2Fconcept~3Fid~3Dstars~252Fsirius",
    );
  });

  it("keeps a literal tilde apart from an escape", () => {
    expect(staticKey("/a~b")).not.toBe(staticKey("/a%7Eb"));
  });
});

describe("searchIndex", () => {
  const concepts = [
    { id: "stars/sirius", title: "Sirius", body: "The brightest star in the night sky." },
    { id: "stars/polaris", title: "Polaris", body: "The North Star, near the pole. Not the brightest." },
    { id: "solar-system/venus", title: "Venus", body: "After the Moon, the brightest natural object." },
  ];

  it("ranks a title match above a body match", () => {
    const res = searchIndex(concepts, "polaris", 20);
    expect(res.results?.map((r) => r.id)).toEqual(["stars/polaris"]);
    const bright = searchIndex(concepts, "brightest", 20);
    expect(bright.count).toBe(3);
  });

  it("needs every word, ignoring case and accents", () => {
    expect(searchIndex(concepts, "NÓRTH star", 20).results?.map((r) => r.id)).toEqual(["stars/polaris"]);
    expect(searchIndex(concepts, "north venus", 20).count).toBe(0);
  });

  it("returns nothing for an empty query and honours the limit", () => {
    expect(searchIndex(concepts, "  ", 20).count).toBe(0);
    expect(searchIndex(concepts, "the", 2).count).toBe(2);
  });
});
