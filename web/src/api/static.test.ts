import { LIVE_ORIGIN_POLL_MS, LIVE_POLL_MS, pollIntervalMs, probeSource, searchIndex, staticKey } from "./static";

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

describe("probeSource (D800)", () => {
  const answer = (init: ResponseInit, body: unknown) =>
    (async () => new Response(JSON.stringify(body), init)) as unknown as typeof fetch;

  it("is live when the origin lists its KBs, and asks the API path once", async () => {
    const fetchFn = vi.fn(answer({ status: 200 }, { kbs: [{ name: "astronomy" }, { name: "guestbook" }] }));
    expect(await probeSource("https://demo.example.com", fetchFn)).toBe("live");
    expect(fetchFn).toHaveBeenCalledTimes(1);
    expect(fetchFn.mock.calls[0]?.[0]).toBe("https://demo.example.com/api/ui/v1/kbs");
  });

  it("is the snapshot when there is no origin, without asking anything", async () => {
    const fetchFn = vi.fn();
    expect(await probeSource("", fetchFn)).toBe("snapshot");
    expect(fetchFn).not.toHaveBeenCalled();
  });

  it("is the snapshot on an error status, a body that is not a KB list, or a network error", async () => {
    expect(await probeSource("https://demo.example.com", answer({ status: 503 }, {}))).toBe("snapshot");
    expect(await probeSource("https://demo.example.com", answer({ status: 200 }, { hello: 1 }))).toBe("snapshot");
    const down = (async () => {
      throw new TypeError("failed to fetch");
    }) as unknown as typeof fetch;
    expect(await probeSource("https://demo.example.com", down)).toBe("snapshot");
  });

  it("is the snapshot when the origin does not answer within the budget", async () => {
    const hang = ((_url: string, init?: RequestInit) =>
      new Promise((_, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      })) as unknown as typeof fetch;
    expect(await probeSource("https://demo.example.com", hang, 20)).toBe("snapshot");
  });
});

describe("pollIntervalMs (D800)", () => {
  it("is 20 s only in a live session on a build with a live origin", () => {
    expect(pollIntervalMs("live", "https://demo.example.com")).toBe(LIVE_ORIGIN_POLL_MS);
    expect(LIVE_ORIGIN_POLL_MS).toBe(20_000);
  });

  it("stays at 10 s for a snapshot, an undecided source, and a server's own Atlas", () => {
    expect(pollIntervalMs("snapshot", "https://demo.example.com")).toBe(LIVE_POLL_MS);
    expect(pollIntervalMs(null, "https://demo.example.com")).toBe(LIVE_POLL_MS);
    expect(pollIntervalMs("live", "")).toBe(LIVE_POLL_MS);
    expect(LIVE_POLL_MS).toBe(10_000);
  });
});
