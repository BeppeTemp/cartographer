import { describe, expect, it } from "vitest";
import type { SecretFile } from "../api/types";
import { groupByService } from "../components/Secrets";

const file = (path: string, pointers: [string, string[]][], source_of: string[] = []): SecretFile => ({
  path,
  recipients: [],
  age_stanzas: [],
  source_of,
  entries: pointers.map(([pointer, referenced_by]) => ({ pointer, aad_path: "", enc: `${path}${pointer}`, referenced_by })),
});

describe("groupByService", () => {
  it("puts each value under the Service that uses it, then the values no Service uses", () => {
    const files = [
      file("secrets/a.sops.yaml", [
        ["/api/token", ["services/api"]],
        ["/shared/key", ["services/api", "services/db"]],
        ["/note", ["infra/x"]],
      ]),
      file("secrets/env.sops.yaml", [["/KEY", []]], ["services/db"]),
    ];
    const groups = groupByService(files, [
      { id: "services/api", title: "API" },
      { id: "services/db", title: "DB" },
      { id: "services/idle", title: "Idle" },
    ]);
    expect(groups.map((g) => [g.service?.id ?? "", g.rows.map((r) => r.entry.pointer)])).toEqual([
      ["services/api", ["/api/token", "/shared/key"]],
      ["services/db", ["/shared/key", "/KEY"]],
      ["", ["/note"]],
    ]);
  });
});
