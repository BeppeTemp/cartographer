import { HIDDEN_OUT_OF_MAP } from "./fixture.mjs";
import { LOCAL_URL, expect, test, waitForAtlas } from "./support";

// Every test in this suite also fails if the page contacts any origin other
// than the server under test (support.ts): the assertions below add the ones
// that need a page of their own.
test.use({ reducedMotion: "reduce" });

test("the shell carries the CSP, and the CSP blocks inline and evaluated script", async ({ page }) => {
  const response = await page.goto(`${LOCAL_URL}/ui/?kb=atlas`);
  const csp = response?.headers()["content-security-policy"] ?? "";
  const scriptSrc = csp.split(";").map((d) => d.trim()).find((d) => d.startsWith("script-src")) ?? "";
  expect(scriptSrc).toBe("script-src 'self'");
  expect(csp).toContain("default-src 'self'");
  expect(csp).toContain("connect-src 'self'");
  expect(csp).toContain("object-src 'none'");
  expect(csp).not.toContain("unsafe-eval");
  await waitForAtlas(page);

  // Script injected through page.evaluate runs over the DevTools protocol,
  // which the browser exempts from CSP, so eval is probed from a same-origin
  // script the policy does allow: only its eval can be refused.
  await page.route(`${LOCAL_URL}/ui/__csp-probe.js`, (route) =>
    route.fulfill({
      contentType: "text/javascript",
      body: 'try { eval("1"); document.documentElement.dataset.evalProbe = "ran"; } catch { document.documentElement.dataset.evalProbe = "blocked"; }',
    }),
  );
  const outcome = await page.evaluate(async () => {
    const violations: string[] = [];
    document.addEventListener("securitypolicyviolation", (event) => violations.push(event.violatedDirective));
    const inline = document.createElement("script");
    inline.textContent = "window.__cartographerInline = true;";
    document.body.appendChild(inline);
    const probe = document.createElement("script");
    probe.src = "/ui/__csp-probe.js";
    await new Promise((resolve) => {
      probe.onload = resolve;
      probe.onerror = resolve;
      document.body.appendChild(probe);
    });
    await new Promise((resolve) => setTimeout(resolve, 100));
    return {
      inline: (window as unknown as { __cartographerInline?: boolean }).__cartographerInline === true,
      evaluated: document.documentElement.dataset.evalProbe,
      violations,
    };
  });
  expect(outcome.inline).toBe(false);
  expect(outcome.evaluated).toBe("blocked");
  expect(outcome.violations.some((d) => d.startsWith("script-src"))).toBe(true);
});

test("a hostile concept body renders inert", async ({ page }) => {
  await page.goto(`${LOCAL_URL}/ui/?kb=atlas&scope=apps&concept=${encodeURIComponent(HIDDEN_OUT_OF_MAP)}`);
  const inspector = page.getByRole("complementary", { name: `Inspector for ${HIDDEN_OUT_OF_MAP}` });
  await expect(inspector.getByText("The reverse proxy in front of every service.")).toBeVisible();

  expect(await inspector.locator("script").count()).toBe(0);
  expect(await inspector.locator("[onerror]").count()).toBe(0);
  expect(await inspector.locator("img").count()).toBe(0);
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => (window as unknown as { __cartographerXSS?: string }).__cartographerXSS)).toBeUndefined();
});
