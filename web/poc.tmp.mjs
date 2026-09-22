import { chromium } from "@playwright/test";
const [file, out, w, h, wait] = process.argv.slice(2);
const browser = await chromium.launch({ headless: false });
const page = await browser.newPage({ viewport: { width: +w, height: +h } });
await page.goto("file://" + file);
await page.waitForTimeout(+wait || 5000);
await page.screenshot({ path: out, fullPage: true });
await browser.close();
