import { useEffect, useState } from "react";
import { STATIC_MODE } from "../api/static";
import { brandingURL, fetchBranding, fetchBrandingFile } from "../api/client";

/** What the top bar shows of a branding plugin (the Atlas branding decision). */
export interface BrandLogo {
  company: string;
  light: string;
  dark: string;
}

/** A branding no longer carries a favicon: the tab keeps Cartographer's own. A link an earlier version set is removed. */
const ICON_ID = "branding-favicon";

function dropBrandingFavicon() {
  document.getElementById(ICON_ID)?.remove();
}

/**
 * The last branding seen, as data: URLs, so the next load shows the logo
 * with the first paint instead of popping them in after the request.
 * The server's answer still decides: a changed version replaces it, a 404
 * clears it. Browser storage can be missing (private mode): then it just waits.
 */
const CACHE_KEY = "cartographer.branding";

/** `dark` is stored only when it differs from `light`; a missing one reads as the light logo. */
interface Cached {
  company: string;
  light: string;
  dark?: string;
  version: string;
}

const logoOf = (c: Cached): BrandLogo => ({ company: c.company, light: c.light, dark: c.dark ?? c.light });

function readCache(): Cached | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY);
    const c = raw ? (JSON.parse(raw) as Partial<Cached> | null) : null;
    const data = (v: unknown): v is string => typeof v === "string" && v.startsWith("data:");
    if (!c || !data(c.light) || (c.dark !== undefined && !data(c.dark)) || typeof c.company !== "string" || typeof c.version !== "string") {
      return null;
    }
    return { company: c.company, light: c.light, ...(c.dark !== undefined && c.dark !== c.light ? { dark: c.dark } : {}), version: c.version };
  } catch {
    return null;
  }
}

function writeCache(c: Cached | null) {
  try {
    if (c) localStorage.setItem(CACHE_KEY, JSON.stringify(c));
    else localStorage.removeItem(CACHE_KEY);
  } catch {
    /* storage full or blocked: the next load waits for the server */
  }
}

/**
 * loadBranding asks the server for the branding and applies it: the logos as data: URLs (they are
 * cached for the next load, and an <img> could not carry a bearer token anyway). No branding (404) clears the cache
 * and resolves to null; the static demo, or a failure that is not a 404, keeps what is shown.
 */
export async function loadBranding(signal?: AbortSignal): Promise<BrandLogo | null | undefined> {
  if (STATIC_MODE) return null;
  dropBrandingFavicon();
  try {
    const b = await fetchBranding(signal);
    if (!b || typeof b.version !== "string") {
      writeCache(null);
      return null;
    }
    const cached = readCache();
    if (cached && cached.version === b.version) {
      return logoOf(cached);
    }
    const file = (path: Parameters<typeof brandingURL>[0]) => fetchBrandingFile(brandingURL(path, b.version), signal);
    if (!b.logo_light) {
      writeCache(null);
      return null;
    }
    const light = await file("branding/logo-light");
    const dark = b.logo_dark && b.logo_dark !== b.logo_light ? await file("branding/logo-dark") : light;
    writeCache({ company: b.company, light, ...(dark !== light ? { dark } : {}), version: b.version });
    return { company: b.company, light, dark };
  } catch {
    return undefined;
  }
}

/** useBranding shows the cached branding at once, then what the server says (once per page load). */
export function useBranding(): BrandLogo | null {
  const [logo, setLogo] = useState<BrandLogo | null>(() => {
    const c = STATIC_MODE ? null : readCache();
    return c ? logoOf(c) : null;
  });
  useEffect(() => {
    const ctl = new AbortController();
    void loadBranding(ctl.signal).then((l) => {
      if (!ctl.signal.aborted && l !== undefined) setLogo(l);
    });
    return () => ctl.abort();
  }, []);
  return logo;
}
