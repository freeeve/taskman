# 012 -- features view in the web ui

Opened 2026-07-10.

## Outcome

Features tab in the web UI: cards with linked-task chips colored by
ledger status (chips open the task dialog), collapsible rendered spec,
ship-it button, + feature creation. POST features and
features/{slug}/done mirror the CLI commits; store.NewFeature shared
between cmdFeatureNew and the API. Covered by TestAPIFeatureMutations.
