import "@testing-library/jest-dom/vitest";

/*
 * Two things jsdom does not give us, stubbed here so a test failure always
 * means the code is wrong rather than the environment being thin.
 *
 * matchMedia: absent in jsdom. Theme and reduced-motion both read it.
 *
 * localStorage / sessionStorage: Node 26 defines its own experimental
 * `localStorage` global that shadows jsdom's and evaluates to `undefined`
 * unless the process was started with --localstorage-file. The property is
 * configurable, so a conforming in-memory Storage is installed over it. Tests
 * assert what *this* code writes and never writes, which is exactly what an
 * in-memory Storage can answer.
 */

if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

function memoryStorage(): Storage {
  let entries = new Map<string, string>();
  const storage: Storage = {
    get length() {
      return entries.size;
    },
    clear: () => {
      entries = new Map();
    },
    getItem: (key) => entries.get(String(key)) ?? null,
    key: (index) => [...entries.keys()][index] ?? null,
    removeItem: (key) => {
      entries.delete(String(key));
    },
    setItem: (key, value) => {
      entries.set(String(key), String(value));
    },
  };
  // JSON.stringify(localStorage) is how the token tests assert "this string is
  // nowhere in storage", and that walks own enumerable properties.
  return new Proxy(storage, {
    ownKeys: () => [...entries.keys()],
    getOwnPropertyDescriptor: (_target, prop) =>
      typeof prop === "string" && entries.has(prop)
        ? { value: entries.get(prop), enumerable: true, configurable: true, writable: true }
        : undefined,
    get: (target, prop, receiver) =>
      typeof prop === "string" && entries.has(prop)
        ? entries.get(prop)
        : Reflect.get(target, prop, receiver),
  });
}

for (const name of ["localStorage", "sessionStorage"] as const) {
  if (!globalThis[name]) {
    const value = memoryStorage();
    Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
    if (globalThis !== (window as unknown as typeof globalThis)) {
      Object.defineProperty(window, name, { value, configurable: true, writable: true });
    }
  }
}
