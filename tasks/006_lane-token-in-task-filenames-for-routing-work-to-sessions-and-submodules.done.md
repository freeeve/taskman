# 006 -- lane token in task filenames for routing work to sessions and submodules

Opened 2026-07-10.

## Outcome

Grammar: NUM[-lane]_slug; lane is a free-form token after the maximal
leading digit run (heads without leading digits remain legacy
prefixes, so qbd-impl stays a prefix). Lane sits in the stem, so every
rename path preserves it for free. Added Task.Lane, Task.SetLane,
new -lane, list -lane + lane column, and the lane set/clear command.
Covered by parse table rows, a lane-through-lifecycle test, CLI test,
and fuzz seeds (10s fuzz run clean).
