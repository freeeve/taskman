# 007 -- per-project order file for priority plus top command

Opened 2026-07-10.

## Outcome

internal/store/order.go: ReadOrder (lenient, advisory, fuzzed),
WriteOrder (whole-file rewrite, one commit per reorder), PruneOrder
(only when the file exists and changes), SortByOrder (listed first in
file order, unlisted after in ledger order). list sorts by it; new top
command prints the highest-priority pending undeferred task with -lane
filter; done prunes its number in the same commit; fix prunes stale
entries; migrate reuses WriteOrder.
