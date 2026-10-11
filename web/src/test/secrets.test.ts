import { generateIdentity } from "age-encryption";
import { describe, expect, it } from "vitest";
import { decryptEntry, decryptFile, InvalidKeyError, looksLikeAgeKey, openDataKey, WrongKeyError } from "../lib/secrets";
import { FIXTURE_FILE, FIXTURE_KEY, FIXTURE_PLAIN } from "./secretsFixture";

describe("opening sops values in the browser", () => {
  it("decrypts every value of a real sops file with the right additional data", async () => {
    const got = await decryptFile(FIXTURE_KEY, FIXTURE_FILE);
    expect(Object.fromEntries(got)).toEqual(FIXTURE_PLAIN);
  });

  it("authenticates the path: the wrong additional data fails", async () => {
    const key = await openDataKey(FIXTURE_KEY, FIXTURE_FILE.age_stanzas);
    const e = FIXTURE_FILE.entries.find((x) => x.pointer === "/demo/ports/0")!;
    await expect(decryptEntry(key, e)).resolves.toBe("8080");
    await expect(decryptEntry(key, { ...e, aad_path: "demo:ports:0:" })).rejects.toBeDefined();
  });

  it("rejects a key that is not a recipient, with a clear error", async () => {
    const other = await generateIdentity();
    await expect(openDataKey(other, FIXTURE_FILE.age_stanzas)).rejects.toBeInstanceOf(WrongKeyError);
    await expect(openDataKey("AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQ", FIXTURE_FILE.age_stanzas)).rejects.toBeInstanceOf(
      InvalidKeyError,
    );
    expect(looksLikeAgeKey(FIXTURE_KEY)).toBe(true);
    expect(looksLikeAgeKey("not a key")).toBe(false);
  });
});
