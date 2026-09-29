import { withoutRedundantLinksSection as strip } from "./linksection";

const body = (section: string) => `Intro.\n\n## Punti aperti\n\n- one\n\n## Collegamenti\n\n${section}\n`;

describe("withoutRedundantLinksSection", () => {
  it("drops a links-only section whose relative links are all outbound", () => {
    const out = strip(body("- [Keycloak](../piattaforma/keycloak.md)\n- [Inc](../incidenti/x.md)"), "attivita/k", [
      "piattaforma/keycloak",
      "incidenti/x",
    ]);
    expect(out).not.toContain("Collegamenti");
    expect(out).toContain("Punti aperti");
  });
  it("drops a wiki-link section covered by the outbound links", () => {
    expect(strip(body("- [[a/b]]\n- [[c/d|Label]]"), "x/y", ["a/b", "c/d"])).not.toContain("Collegamenti");
  });
  it("keeps it when one link is not among the outbound links", () => {
    expect(strip(body("- [[a/b]]\n- [[z/z]]"), "x/y", ["a/b"])).toContain("Collegamenti");
  });
  it("keeps it when it carries authored text", () => {
    expect(strip(body("- [[a/b]] — why it matters"), "x/y", ["a/b"])).toContain("Collegamenti");
  });
  it("keeps it when it links outside the KB", () => {
    expect(strip(body("- [Docs](https://example.com)"), "x/y", [])).toContain("Collegamenti");
  });
});
