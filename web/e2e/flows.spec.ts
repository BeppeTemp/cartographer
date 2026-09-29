import { expect, listedConcepts, LOCAL_URL, openConcept, selectedIn, test, waitForAtlas, withPanelsOpen } from "./support";

// The flows a sighted mouse user takes, against the auth-off server. Motion is
// reduced for the whole file: the assertions are about state, and a camera
// mid-tween or a node mid-settle is a source of timing, not of coverage.
test.use({ reducedMotion: "reduce" });
test.beforeEach(({ page }) => withPanelsOpen(page));

const ATLAS = `${LOCAL_URL}/ui/?kb=atlas`;
const INFRA = ["infra/cluster", "infra/cluster/nodes", "infra/dns", "infra/firewall", "infra/gateway", "infra/legacy-vpn"];

test("local mode reaches the atlas with no prompt", async ({ page }) => {
  await page.goto(`${LOCAL_URL}/`);
  await expect(page).toHaveURL(/\/ui\//);
  await waitForAtlas(page);
  await expect(page.getByLabel("Bearer token")).toHaveCount(0);
  // Connected is the normal state: the status only takes room when it is not.
  await expect(page.getByText("Disconnected")).toBeHidden();
});

test("switching KB changes the graph and the overview", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await expect(page.getByRole("button", { name: /Infrastructure/ })).toBeVisible();
  expect(await listedConcepts(page)).toContain("infra/gateway");

  await page.getByRole("combobox", { name: "Knowledge Base" }).click();
  await page.getByRole("option", { name: "annex" }).click();
  await expect(page).toHaveURL(/kb=annex/);
  await expect(page.getByRole("button", { name: /Library/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Infrastructure/ })).toHaveCount(0);
  await expect.poll(() => listedConcepts(page)).toEqual(["library/field-guide", "library/reading-list"]);
});

test("selecting a Map loads its scoped graph", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  expect(await listedConcepts(page)).toContain("incidents/2026-01-10-outage");

  await page.getByRole("button", { name: /Infrastructure/ }).click();
  await expect(page).toHaveURL(/scope=infra/);
  await expect.poll(() => listedConcepts(page)).toEqual(INFRA);
  await expect(page.getByText(/^6 nodes ·/)).toBeVisible();
});

test("type and status filters update the graph and the count", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra`);
  await waitForAtlas(page);
  const types = page.getByRole("navigation", { name: "Atlas navigation" });

  await types.getByRole("button", { name: /^Runbook/ }).click();
  await expect.poll(() => listedConcepts(page)).toEqual(["infra/firewall"]);

  await types.getByRole("button", { name: "Clear 1 filter" }).click();
  await types.getByRole("button", { name: /^deprecated/ }).click();
  await expect.poll(() => listedConcepts(page)).toEqual(["infra/legacy-vpn"]);

  await types.getByRole("button", { name: "Clear 1 filter" }).click();
  await expect.poll(() => listedConcepts(page)).toEqual(INFRA);
});

test("a filter that hides the selection keeps it open but draws no names", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
  await waitForAtlas(page);
  await expect(page.locator(".graph3d__label--selected")).toHaveText("Gateway");

  const rail = page.getByRole("navigation", { name: "Atlas navigation" });
  await rail.getByRole("button", { name: /^Runbook/ }).click();
  await expect.poll(() => listedConcepts(page)).toEqual(["infra/firewall"]);
  await expect(page.locator(".graph3d__labels .graph3d__label")).toHaveCount(0);
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
  await expect(page).toHaveURL(/concept=infra%2Fgateway/);
});

test("dragging the reading panel's edge resizes it, and the width survives a reload", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
  const handle = page.getByRole("separator", { name: "Resize reading panel" });
  const panel = page.locator("#reading-panel");
  await expect(handle).toHaveAttribute("aria-valuenow", "420");
  const before = (await panel.boundingBox())!.width;

  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 - 100, box.y + box.height / 2, { steps: 5 });
  await page.mouse.up();
  await expect(handle).toHaveAttribute("aria-valuenow", "520");
  await expect.poll(async () => Math.round((await panel.boundingBox())!.width - before)).toBe(100);

  await page.reload();
  await expect(page.getByRole("separator", { name: "Resize reading panel" })).toHaveAttribute("aria-valuenow", "520");
});

// #436: what a wider reading panel must keep true — the close button at the
// head's right edge, the selection framed in the strip the panel now leaves,
// and no panel wider than its content can use (D281).
test("a wider reading panel keeps its close button right and reframes the selection", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
  await waitForAtlas(page);
  const handle = page.getByRole("separator", { name: "Resize reading panel" });
  const panel = page.locator("#reading-panel");
  const label = page.locator(".graph3d__label--selected");
  await expect(handle).toHaveAttribute("aria-valuenow", "420");
  await expect(label).toHaveText("Gateway");
  const centre = async () => {
    const b = (await label.boundingBox())!;
    return b.x + b.width / 2;
  };
  // The focus lands at once under reduced motion; let the layout settle.
  await page.waitForTimeout(500);
  const before = await centre();

  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  // 150px: inside the measure's cap (D281) in any font the runner has.
  await page.mouse.move(box.x + box.width / 2 - 150, box.y + box.height / 2, { steps: 5 });
  await page.mouse.up();
  await expect(handle).toHaveAttribute("aria-valuenow", "570");

  // The strip's centre moved left by half the widening.
  await expect.poll(async () => Math.abs((await centre()) - (before - 75)), { timeout: 5_000 }).toBeLessThan(20);
  const labelBox = (await label.boundingBox())!;
  expect(labelBox.x + labelBox.width).toBeLessThan((await panel.boundingBox())!.x);

  const head = (await page.locator(".inspector__head").boundingBox())!;
  const close = (await page.getByRole("button", { name: "Close inspector" }).boundingBox())!;
  // The head's inline padding (--space-4) is all that separates them.
  expect(Math.round(head.x + head.width - (close.x + close.width))).toBeLessThanOrEqual(17);
});

test("the reading panel stops growing where its prose stops", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
  await waitForAtlas(page);
  const handle = page.getByRole("separator", { name: "Resize reading panel" });
  // What the window alone would allow: main's width less the graph kept visible.
  const room = await page.evaluate(() => document.querySelector("#main")!.clientWidth - 280);
  const max = Number(await handle.getAttribute("aria-valuemax"));
  // 1440px wide, the window alone would allow far more than the measure.
  expect(max).toBeLessThan(room);

  // Home moves a start-edge handle to the far left: the widest panel.
  await handle.focus();
  await page.keyboard.press("Home");
  await expect(handle).toHaveAttribute("aria-valuenow", String(max));
  const panel = (await page.locator("#reading-panel").boundingBox())!;
  const prose = (await page.locator("#reading-panel .markdown").boundingBox())!;
  // Only the body's padding and the border/scrollbar allowance are left over.
  expect(panel.width - prose.width).toBeLessThanOrEqual(2 * 16 + 24 + 1);
});

test("the Artifacts panel lists what the KB ships and opens a skill", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await page.getByRole("button", { name: /^Artifacts, \d+/ }).click();
  const panel = page.getByRole("region", { name: "Artifacts" });
  await expect(panel.getByRole("heading", { level: 2 })).toHaveText([
    /^Skills/,
    /^Subagents/,
    /^Hooks/,
    /^Instructions/,
    /^Templates/,
  ]);

  await panel.getByRole("button", { name: /review/ }).click();
  await expect(page).toHaveURL(/panel=artifacts&artifact=skill%2Freview/);
  const detail = page.getByRole("article", { name: "Artifact skill/review" });
  await expect(detail.getByRole("heading", { name: "Review", exact: true })).toBeVisible();
  await detail.getByRole("tab", { name: "checklist.txt" }).click();
  await expect(detail.locator("pre")).toContainText("1. tests");

  await page.goBack();
  await expect(page).not.toHaveURL(/artifact=/);
  await expect(page.getByRole("article", { name: /^Artifact / })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: /review/ })).toBeVisible();
});

test("the Observatory's severity floor updates its count", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=observatory`);
  const observatory = page.getByRole("region", { name: "Observatory" });
  await expect(observatory.getByRole("button", { name: /broken_link/ }).first()).toBeVisible();
  const before = await observatory.locator(".observatory__finding").count();
  expect(before).toBeGreaterThan(0);

  await observatory.getByRole("group", { name: "Minimum severity" }).getByRole("button", { name: "Errors only" }).click();
  await expect(observatory.getByText("Nothing to report")).toBeVisible();
  await expect(observatory.getByText(`Showing 0 of ${before} findings`)).toBeVisible();
});

test("the command palette finds a concept and reveals it", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await page.keyboard.press("ControlOrMeta+k");
  const palette = page.getByRole("dialog", { name: "Search concepts" });
  await expect(palette).toBeVisible();
  await palette.getByRole("textbox").fill("gateway");
  await expect(palette.getByRole("option").first()).toContainText("infra/gateway");
  await page.keyboard.press("Enter");

  await expect(palette).toBeHidden();
  await expect(page).toHaveURL(/concept=infra%2Fgateway/);
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
  await expect(page).toHaveURL(selectedIn("infra/gateway"));
});

test("URL state and Back/Forward restore KB, scope and selection", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await page.getByRole("button", { name: /Infrastructure/ }).click();
  await openConcept(page, "infra/dns");
  await expect(page).toHaveURL(/kb=atlas&scope=infra&concept=infra%2Fdns/);

  await page.goBack();
  await expect(page).toHaveURL(/scope=infra$/);
  await expect(page.getByRole("complementary")).toHaveCount(0);

  await page.goBack();
  await expect(page).not.toHaveURL(/scope=/);
  await expect(page.getByRole("button", { name: /Whole atlas/ })).toHaveAttribute("aria-current", "true");

  await page.goForward();
  await page.goForward();
  await expect(page.getByRole("complementary", { name: "Inspector for infra/dns" })).toBeVisible();

  // A deep link opens straight onto the same view.
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fdns`);
  await expect(page.getByRole("complementary", { name: "Inspector for infra/dns" })).toBeVisible();
  await expect.poll(() => listedConcepts(page)).toEqual(INFRA);
});

test("a backlink chip moves the selection to that concept", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
  const inspector = page.getByRole("complementary", { name: "Inspector for infra/gateway" });
  await inspector.getByRole("tab", { name: /Links/ }).click();
  await inspector.getByRole("button", { name: "infra/dns" }).first().click();
  await expect(page.getByRole("complementary", { name: "Inspector for infra/dns" })).toBeVisible();
  await expect(page).toHaveURL(/concept=infra%2Fdns/);
});

test("the inspector names a broken target as having no node", async ({ page }) => {
  await page.goto(`${ATLAS}&scope=infra&concept=infra%2Ffirewall`);
  const inspector = page.getByRole("complementary", { name: "Inspector for infra/firewall" });
  await inspector.getByRole("tab", { name: /Links/ }).click();
  await expect(inspector.locator(".chip--broken")).toHaveText(/infra\/missing-ruleset/);
  await expect(inspector.getByText("they have no node in the graph")).toBeVisible();
});

test("an Observatory finding reveals its concept, or explains there is none", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=observatory`);
  const observatory = page.getByRole("region", { name: "Observatory" });

  // A finding about a map's own index is about no concept.
  await observatory.getByRole("button", { name: /infra\/index\.md/ }).click();
  await expect(page.getByRole("status").filter({ hasText: "no node to reveal" })).toBeAttached();
  await expect(observatory).toBeVisible();

  await observatory.getByRole("button", { name: /infra\/firewall\.md/ }).click();
  await expect(page).toHaveURL(/concept=infra%2Ffirewall/);
  await expect(page.getByRole("complementary", { name: "Inspector for infra/firewall" })).toBeVisible();
  await expect(page).toHaveURL(selectedIn("infra/firewall"));
});

test("the Observatory follows the rail's Map, and hides the node filters (#364)", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=observatory`);
  const observatory = page.getByRole("region", { name: "Observatory" });
  const rail = page.getByRole("navigation", { name: "Atlas navigation" });
  await expect(observatory.getByRole("button", { name: /infra\/firewall\.md/ })).toBeVisible();
  // Type and Status filter nodes, not findings: they step aside here.
  await expect(rail.getByRole("heading", { name: "Type" })).toHaveCount(0);

  // Another Map's findings leave the list, and the page names the scope.
  await rail.getByRole("button", { name: /Applications/ }).click();
  await expect(observatory.getByRole("heading", { level: 1 })).toContainText("in Applications");
  await expect(observatory.getByRole("button", { name: /infra\/firewall\.md/ })).toHaveCount(0);

  await rail.getByRole("button", { name: /Infrastructure/ }).click();
  await expect(observatory.getByRole("heading", { level: 1 })).toContainText("in Infrastructure");
  await expect(observatory.getByRole("button", { name: /infra\/firewall\.md/ })).toBeVisible();
  const paths = await observatory.locator(".observatory__path").allTextContents();
  expect(paths.every((path) => path.startsWith("infra/"))).toBe(true);

  // Whole atlas restores the KB-wide list; the Atlas gets its filters back.
  await rail.getByRole("button", { name: /Whole atlas/ }).click();
  await expect(observatory.getByRole("heading", { level: 1 })).not.toContainText("Infrastructure");
  await expect(observatory.getByText(/run over the whole KB/)).toBeVisible();
  await rail.getByRole("button", { name: "Atlas", exact: true }).click();
  await expect(rail.getByRole("heading", { name: "Type" })).toBeVisible();
});

test("a truncated graph says so and names how to narrow it", async ({ page }) => {
  await page.route("**/api/ui/v1/kbs/atlas/graph**", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    await route.fulfill({ response, json: { ...body, truncated: true, total_nodes: body.nodes.length + 40 } });
  });
  await page.goto(ATLAS);
  const banner = page.locator(".graph__banner");
  await expect(banner).toContainText(/Showing 9 of 49 concepts/);
  await expect(banner).toContainText("narrow it by Map, type or status");
});

test("an empty KB renders its designed state", async ({ page }) => {
  await page.goto(`${LOCAL_URL}/ui/?kb=void`);
  await expect(page.getByText("This KB has no concepts yet")).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Atlas navigation" })).toBeVisible();
});

test("a 500 stays inside the graph panel", async ({ page }) => {
  await page.route("**/api/ui/v1/kbs/atlas/graph**", (route) =>
    route.fulfill({ status: 500, json: { error: { code: "internal", message: "internal error" } } }),
  );
  await page.goto(ATLAS);
  const main = page.getByRole("main");
  await expect(main.getByRole("alert")).toContainText("Server error");
  await expect(page.getByRole("navigation", { name: "Atlas navigation" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Infrastructure/ })).toBeVisible();
  await expect(page.getByRole("complementary")).toHaveCount(0);
});

test("an unreachable server is named, not blank", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await page.route("**/api/ui/v1/**", (route) => route.abort("connectionrefused"));
  await page.getByRole("button", { name: /Infrastructure/ }).click();
  await expect(page.getByRole("main").getByRole("alert")).toContainText("Server unreachable");
  await expect(page.getByRole("status").filter({ hasText: "Disconnected" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Atlas navigation" })).toBeVisible();
});
