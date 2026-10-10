import { expect, listedConcepts, LOCAL_URL, openConcept, selectedIn, test, waitForAtlas, withPanelsOpen } from "./support";
import type { Locator } from "@playwright/test";

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
  await page.getByRole("button", { name: "Artifacts", exact: true }).click();
  const panel = page.getByRole("region", { name: "Artifacts" });
  const nav = panel.getByRole("navigation", { name: "Artifacts by kind" });
  await expect(nav.getByRole("heading", { level: 2 })).toHaveText([
    /Skills/,
    /Subagents/,
    /Hooks/,
    /Instructions/,
    /Templates/,
  ]);

  await nav.getByRole("button", { name: /review/ }).click();
  await expect(page).toHaveURL(/panel=artifacts&artifact=skill%2Freview/);
  const detail = page.getByRole("article", { name: "Artifact skill/review" });
  await expect(detail.getByRole("heading", { name: "Review", exact: true })).toBeVisible();
  await detail.getByRole("tab", { name: "checklist.txt" }).click();
  await expect(detail.locator("pre")).toContainText("1. tests");

  await page.goBack();
  await expect(page).not.toHaveURL(/artifact=/);
  await expect(page.getByRole("article", { name: /^Artifact / })).toHaveCount(0);
  await expect(nav.getByRole("button", { name: /review/ })).toBeVisible();
});

/** Health folds each check's findings into a row (D365): open them all.
 *  With a target, retry until it shows: a scope change re-renders the rows
 *  closed once its findings arrive. Without one the scope may have no
 *  findings at all, and then Health drops the section: there is no row to wait for. */
async function openFindings(health: Locator, target?: Locator) {
  await expect(async () => {
    if (target) await expect(health.locator(".health__check").first()).toBeVisible({ timeout: 1000 });
    await health
      .locator(".health__check details")
      .evaluateAll((els) => els.forEach((el) => ((el as HTMLDetailsElement).open = true)));
    if (target) await expect(target).toBeVisible({ timeout: 1000 });
  }).toPass();
}

test("Health loads the upkeep schedule and the questions, and offers no write", async ({ page }) => {
  await page.goto(ATLAS);
  await waitForAtlas(page);
  await page.getByRole("button", { name: /^Health/ }).click();
  await expect(page).toHaveURL(/panel=health/);
  const panel = page.getByRole("region", { name: "Health" });
  // A KB served with no doctor settings is maintained by default (D323).
  const automatic = panel.getByRole("region", { name: /Automatic/ });
  await expect(automatic).toContainText("daily");
  // The fixture has no open question: its lane says so, no empty section.
  await expect(panel.getByText("Nothing waits on you.")).toBeVisible();
  await expect(panel.getByRole("heading", { name: /^Questions for you/ })).toHaveCount(0);
  await expect(panel.getByRole("heading", { name: /^Done by Cartographer/ })).toBeVisible();
  await expect(panel.getByText(/Could not read/)).toHaveCount(0);
  // The state word stays inside the ring: within its inner chord (~85px at
  // the word's height, the ring being 136px wide) and centred on it.
  const ring = (await panel.locator(".state-ring").boundingBox())!;
  const word = (await panel.locator(".state-ring__word").boundingBox())!;
  expect(word.width).toBeLessThanOrEqual(85);
  expect(word.x).toBeGreaterThanOrEqual(ring.x);
  expect(word.x + word.width).toBeLessThanOrEqual(ring.x + ring.width);
});

test("Health groups findings by check and lists every check on its own tab (D365)", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=health`);
  const health = page.getByRole("region", { name: "Health" });
  const broken = health.locator("#check-broken_link");
  await expect(broken).toContainText("Broken links");

  await health.getByRole("button", { name: /^Checks · \d+$/ }).click();
  const checks = health.getByRole("region", { name: "Checks" });
  await expect(checks).toContainText("Pages nothing links to");
  await checks.getByRole("button", { name: "Broken links" }).click();
  await expect(broken.locator("details")).toHaveAttribute("open", "");
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
  await expect(page.getByRole("button", { name: /^All\s*\d/ })).toHaveAttribute("aria-current", "true");

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

test("a Health finding reveals its concept, or explains there is none", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=health`);
  const health = page.getByRole("region", { name: "Health" });

  // A finding about a map's own index is about no concept.
  const index = health.getByRole("button", { name: /infra\/index\.md/ }).first();
  await openFindings(health, index);
  await index.click();
  await expect(page.getByRole("status").filter({ hasText: "no node to reveal" })).toBeAttached();
  await expect(health).toBeVisible();

  const firewall = health.getByRole("button", { name: /infra\/firewall/ }).first();
  await openFindings(health, firewall);
  await firewall.click();
  await expect(page).toHaveURL(/concept=infra%2Ffirewall/);
  await expect(page.getByRole("complementary", { name: "Inspector for infra/firewall" })).toBeVisible();
  await expect(page).toHaveURL(selectedIn("infra/firewall"));
});

test("the rail's Maps open the Atlas; Health filters by Map in the page (#364, #697)", async ({ page }) => {
  await page.goto(`${ATLAS}&panel=health`);
  const health = page.getByRole("region", { name: "Health" });
  const rail = page.getByRole("navigation", { name: "Atlas navigation" });
  const chips = health.getByRole("list", { name: "Filter by Map" });
  await openFindings(health, health.getByRole("button", { name: /infra\/firewall/ }).first());
  // Type and Status filter nodes, not findings: they step aside here.
  await expect(rail.getByRole("heading", { name: "Type" })).toHaveCount(0);

  // Another Map's findings leave the list, and the page names the scope.
  await chips.getByRole("button", { name: /Applications/ }).click();
  await expect(health.getByRole("heading", { level: 1 })).toContainText("in Applications");
  await expect(page).toHaveURL(/hmap=/);
  await openFindings(health);
  await expect(health.getByRole("button", { name: /infra\/firewall/ })).toHaveCount(0);

  await chips.getByRole("button", { name: /Infrastructure/ }).click();
  await expect(health.getByRole("heading", { level: 1 })).toContainText("in Infrastructure");
  await openFindings(health, health.getByRole("button", { name: /infra\/firewall/ }).first());
  const paths = [
    ...(await health.locator(".health__path").allTextContents()),
    ...(await health.locator(".health__page").evaluateAll((els) => els.map((el) => el.getAttribute("title") ?? ""))),
  ];
  expect(paths.length).toBeGreaterThan(0);
  expect(paths.every((path) => path.startsWith("infra/"))).toBe(true);

  // Pressing the chip again restores the KB-wide list.
  await chips.getByRole("button", { name: /Infrastructure/ }).click();
  await expect(health.getByRole("heading", { level: 1 })).not.toContainText("Infrastructure");
  await expect(health.getByText(/^Over .* only\.$/)).toHaveCount(0);

  // The rail navigates: a Map opens the Atlas on it, and the node filters return.
  await rail.getByRole("button", { name: /Applications/ }).click();
  await expect(page).not.toHaveURL(/panel=health/);
  await expect(page).toHaveURL(/scope=/);
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
