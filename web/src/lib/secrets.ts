import { armor, Decrypter } from "age-encryption";
import type { SecretEntry } from "../api/types";

/**
 * Opening sops values in the browser. The server sends ciphertext and the
 * additional data each value was sealed with; the reader's age identity never
 * leaves the page. Callers keep the identity in component state only: nothing
 * here stores, logs or sends it.
 *
 * Format (getsops/sops, aes/cipher.go and Tree.Encrypt): a value is
 * `ENC[AES256_GCM,data:<b64>,iv:<b64>,tag:<b64>,type:<t>]`, sealed with
 * AES-256-GCM under the 32-byte data key; the additional data is the map keys
 * from the root joined with ":" plus a trailing ":" (a list item is sealed
 * under the path of the key that holds the list). The data key is itself an
 * age file (`sops.age[].enc`). The file MAC is not verified here: each value
 * is already authenticated by its own GCM tag.
 */

export class WrongKeyError extends Error {
  constructor() {
    super("This key does not open the file: it is not one of the recipients.");
    this.name = "WrongKeyError";
  }
}

export class InvalidKeyError extends Error {
  constructor() {
    super("This is not a valid age secret key (check that it was pasted whole).");
    this.name = "InvalidKeyError";
  }
}

export interface EncValue {
  data: Uint8Array<ArrayBuffer>;
  iv: Uint8Array<ArrayBuffer>;
  tag: Uint8Array<ArrayBuffer>;
  type: string;
}

const ENC = /^ENC\[AES256_GCM,data:([^,]*),iv:([^,]*),tag:([^,]*),type:([a-z]+)\]$/;

function b64(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function parseEnc(enc: string): EncValue {
  const m = ENC.exec(enc);
  if (!m) throw new Error("not an ENC[AES256_GCM,...] value");
  return { data: b64(m[1]!), iv: b64(m[2]!), tag: b64(m[3]!), type: m[4]! };
}

/** Whether a pasted string has the shape of an age secret key. */
export function looksLikeAgeKey(s: string): boolean {
  return /^AGE-SECRET-KEY-1[A-Z0-9]{50,}$/i.test(s.trim());
}

/** Opens the file's data key with the identity, trying each age stanza. */
export async function openDataKey(identity: string, ageStanzas: string[]): Promise<CryptoKey> {
  const d = new Decrypter();
  try {
    d.addIdentity(identity.trim());
  } catch {
    throw new InvalidKeyError();
  }
  for (const stanza of ageStanzas) {
    let raw: Uint8Array;
    try {
      raw = await d.decrypt(armor.decode(stanza), "uint8array");
    } catch {
      continue;
    }
    return crypto.subtle.importKey("raw", raw as Uint8Array<ArrayBuffer>, "AES-GCM", false, ["decrypt"]);
  }
  throw new WrongKeyError();
}

/** Decrypts one value to the text sops would print for it. */
export async function decryptEntry(key: CryptoKey, entry: Pick<SecretEntry, "enc" | "aad_path">): Promise<string> {
  const v = parseEnc(entry.enc);
  const sealed = new Uint8Array(v.data.length + v.tag.length);
  sealed.set(v.data);
  sealed.set(v.tag, v.data.length);
  const plain = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: v.iv, additionalData: new TextEncoder().encode(entry.aad_path), tagLength: 128 },
    key,
    sealed,
  );
  return new TextDecoder().decode(plain);
}

/** Decrypts every value of a file; a value that fails stays out of the map. */
export async function decryptFile(
  identity: string,
  file: { age_stanzas: string[]; entries: SecretEntry[] },
): Promise<Map<string, string>> {
  const key = await openDataKey(identity, file.age_stanzas);
  const out = new Map<string, string>();
  for (const e of file.entries) {
    try {
      out.set(e.pointer, await decryptEntry(key, e));
    } catch {
      /* leave it masked */
    }
  }
  return out;
}

/** The value type sops recorded in the envelope (str, int, float, bool, bytes). */
export function encType(enc: string): string {
  return /,type:([a-z]+)\]$/.exec(enc)?.[1] ?? "?";
}
