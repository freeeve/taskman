# 008 -- features directory as source of truth with feature cli

Opened 2026-07-10.

## Outcome

internal/store/feature.go: Feature type, LoadFeatures (every .md in
features/ is a feature; H1 title, lenient fuzzed Tasks: line), SetDone
rename. CLI: feature new (template), feature list (done-task rollup vs
ledger, shipped hidden without -all), feature done. Task linking is a
body edit by design (no link command in v1).
