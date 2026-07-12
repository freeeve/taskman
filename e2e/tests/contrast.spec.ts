import { test, expect } from "@playwright/test";
import { gotoBoard } from "../helpers";

/**
 * Theme contrast (task 113): the light theme's palette must meet WCAG AA, not
 * just the dark theme. Status-colored column counts and dim/secondary text over
 * the light column background previously failed (amber count 2.7:1, dim text
 * 4.1:1). This computes the sRGB-luminance contrast ratio of key text against
 * its nearest opaque background and asserts AA (>=4.5 normal, >=3 large/bold),
 * forcing the light color scheme so a dark-only regression cannot hide it.
 */

test.use({ colorScheme: "light" });

test("light-theme board text meets WCAG AA contrast (task 113)", async ({ page }) => {
  await gotoBoard(page);

  const failures = await page.evaluate(() => {
    const lum = (r: number, g: number, b: number) => {
      const f = (c: number) => {
        c /= 255;
        return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const nums = (s: string) => (s.match(/\d+(\.\d+)?/g) || []).map(Number);
    const bgOf = (el: Element) => {
      let e: Element | null = el;
      while (e) {
        const p = nums(getComputedStyle(e).backgroundColor);
        if (p.length >= 3 && (p[3] === undefined || p[3] > 0)) return p;
        e = e.parentElement;
      }
      return [255, 255, 255];
    };
    const ratio = (el: Element) => {
      const fg = nums(getComputedStyle(el).color);
      const bg = bgOf(el);
      const L1 = lum(fg[0], fg[1], fg[2]) + 0.05;
      const L2 = lum(bg[0], bg[1], bg[2]) + 0.05;
      return Math.max(L1, L2) / Math.min(L1, L2);
    };
    // Text that previously failed on the light column background: the
    // status-colored counts and the dim card number.
    const targets: Element[] = [
      ...document.querySelectorAll(".column .count"),
      ...document.querySelectorAll(".card .num"),
    ];
    const out: { desc: string; ratio: number; need: number }[] = [];
    for (const el of targets) {
      const cs = getComputedStyle(el);
      const px = parseFloat(cs.fontSize);
      const large = px >= 24 || (parseInt(cs.fontWeight) >= 700 && px >= 18.66);
      const need = large ? 3.0 : 4.5;
      const r = ratio(el);
      if (r < need) {
        const col = el.closest(".column") as HTMLElement | null;
        out.push({ desc: `${el.className}${col ? " in " + col.dataset.status : ""}`, ratio: Math.round(r * 100) / 100, need });
      }
    }
    return out;
  });

  expect(failures, `light-theme contrast failures: ${JSON.stringify(failures)}`).toEqual([]);
});
