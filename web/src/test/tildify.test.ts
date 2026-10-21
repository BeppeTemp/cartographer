import { describe, expect, it } from "vitest";
import { tildify } from "../components/Settings";

describe("tildify", () => {
  it("shows a home directory as ~", () => {
    expect(tildify("/Users/user/projects/app")).toBe("~/projects/app");
    expect(tildify("/home/user/.claude")).toBe("~/.claude");
    expect(tildify("C:\\Users\\user\\AppData")).toBe("~\\AppData");
  });
  it("rewrites a home after a separator and leaves other paths alone", () => {
    expect(tildify("kb-a: /Users/user/x")).toBe("kb-a: ~/x");
    expect(tildify("/srv/data/Users/x")).toBe("/srv/data/Users/x");
    expect(tildify("/usr/local/bin")).toBe("/usr/local/bin");
  });
});
