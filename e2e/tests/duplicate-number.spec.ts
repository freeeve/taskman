import { test, expect } from "@playwright/test";
import {
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  removeRawTaskFile,
  storeIsLocal,
  uniqueDesc,
  writeRawTaskFile,
} from "../helpers";

/**
 * Duplicate-number board resilience (task 099). The allocation lock now
 * prevents new duplicates, but pre-existing ones (from before the fix, or
 * forged on disk) must not brick the board: the bare number is ambiguous, yet
 * each card flags itself and still opens via its file stem. Forging a second
 * file for an existing number needs a direct store write, so this gates on a
 * local store.
 */

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("a duplicate-numbered task flags itself and each half still opens by stem (task 099)", async ({
  page,
  request,
}) => {
  // One real task, then a forged twin sharing its number with a distinct slug
  // and title -- the shape `taskman fix` would later renumber.
  const t = await createTaskViaAPI(request, uniqueDesc("dup-orig"));
  const pad = String(t.num).padStart(3, "0");
  const twinTitle = uniqueDesc("dup-twin");
  const twinFile = writeRawTaskFile(
    `${pad}_e2e-duptwin-${Date.now()}.md`,
    `# ${t.num} -- ${twinTitle}\n\nForged twin for duplicate-number resilience.\n`
  );

  try {
    await gotoBoard(page);
    const cards = page.locator(`.column.pending .card[data-num="${t.num}"]`);
    await expect(cards).toHaveCount(2);

    // Both halves warn and point at the remedy.
    const badges = page.locator(`.card[data-num="${t.num}"] .badge.duplicate`);
    await expect(badges).toHaveCount(2);
    await expect(badges.first()).toContainText("taskman fix");

    // Each card opens its own file (resolved by stem, not the ambiguous number).
    const opened: string[] = [];
    for (const i of [0, 1]) {
      await cards.nth(i).click();
      await expect(page.locator("#task-dialog")).toBeVisible();
      opened.push((await page.locator("#dialog-file").textContent())!.trim());
      await page.locator("#dialog-close").click();
      await expect(page.locator("#task-dialog")).toBeHidden();
    }
    expect(new Set(opened)).toEqual(new Set([t.file, twinFile]));
  } finally {
    removeRawTaskFile(twinFile);
    await finishTask(request, t.num);
  }
});
