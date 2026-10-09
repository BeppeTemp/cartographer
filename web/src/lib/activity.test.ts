import { changeKind, mostWorked, shownReasons } from "./activity";

describe("changeKind", () => {
  it("tells the reader's changes from the server's upkeep", () => {
    expect(changeKind({ change: "added", ops: ["concept_write: a/b"] })).toBe("new");
    expect(changeKind({ change: "modified", ops: ["concept_patch: a/b"] })).toBe("edited");
    expect(changeKind({ change: "modified", ops: ["auto-repair (background)"] })).toBe("maintenance");
    // Upkeep on top of a real edit is still an edit.
    expect(changeKind({ change: "modified", ops: ["auto-repair (background)", "concept_write: a/b"] })).toBe("edited");
    expect(changeKind({ change: "moved", ops: ["concept_move"] })).toBe("reorganised");
    expect(changeKind({ change: "deleted", ops: [] })).toBe("reorganised");
    // Created in the window and edited since: still new to the reader.
    expect(changeKind({ change: "modified", ops: ["concept_patch: a/b"], added: true })).toBe("new");
    // An edit made outside the server carries no op: it is still an edit.
    expect(changeKind({ change: "modified", ops: [] })).toBe("edited");
    // The server's last_edit_at outweighs ops truncated to five.
    expect(changeKind({ change: "modified", ops: ["auto-repair (background)"], last_edit_at: "2026-01-01T00:00:00Z" })).toBe(
      "edited",
    );
  });
});

describe("shownReasons", () => {
  it("drops upkeep reasons unless they are all there is", () => {
    expect(shownReasons({ reasons: ["auto-repair (background)", "incident 7 follow-up"] })).toEqual(["incident 7 follow-up"]);
    expect(shownReasons({ reasons: ["auto-repair (background)"] })).toEqual(["auto-repair (background)"]);
  });
});

describe("mostWorked", () => {
  it("names the concept with the most content operations, upkeep aside", () => {
    const best = mostWorked([
      { id: "a", change: "modified", ops: ["auto-repair (background)", "auto-repair (background)", "auto-repair (background)"] },
      { id: "b", change: "modified", ops: ["concept_write: b", "concept_patch: b"] },
      { id: "c", change: "added", ops: ["concept_write: c"] },
    ]);
    expect(best?.change.id).toBe("b");
    expect(best?.count).toBe(2);
    expect(mostWorked([{ id: "a", change: "modified", ops: ["auto-repair (background)"] }])).toBeNull();
  });
});
