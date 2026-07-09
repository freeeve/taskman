# 002 -- 247 is deferred; v0.4.0 is tagged but unpushed so go install @latest still gets v0.3.0

Filed from libcat on 2026-07-09 (cross-repo ask).

## Done: libcat's 247 is deferred

`taskman defer` did exactly what `tasks/001` asked for.

```
$ taskman defer -reason "maintainer's call: outward-facing publish to GHCR, a pulled tag cannot be recalled" 247
247_publish-the-lcatd-container-image-to-ghcr-from-ci.md -> ...deferred.md

$ taskman list        # 247 absent; footer: "1 deferred (taskman list -all)"
$ taskman list -all   # 247  deferred  publish-the-lcatd-container-image-to-ghcr-from-ci
```

The body's `**DEFERRED (...)**` paragraph is reduced to the part that is not
about status, per your `tasks/262`.

**The flag-not-a-status design is the right call**, and your correction is
accepted: `taskman next` prints the next free *number*, so my acceptance
criterion in `tasks/001` ("`taskman next` must never return one") rested on a
misreading of that command. `list` is where the agent loop actually looks, and
that is where the hold now shows.

## The one thing that is not done: v0.4.0 is not pushed

`tasks/262` tells the reader to pick the feature up with:

```
go install github.com/freeeve/taskman@latest
```

That installs **v0.3.0**, which has no `defer` subcommand. The `v0.4.0` tag
exists in the local checkout, but `main` is **5 commits ahead of `origin/main`**,
so the module proxy has never seen it:

```
$ cd ~/taskman && git status -sb
## main...origin/main [ahead 5]
$ go install github.com/freeeve/taskman@latest
go: downloading github.com/freeeve/taskman v0.3.0
$ taskman help | grep defer     # nothing
```

I worked around it by building from the checkout (`cd ~/taskman && go install .`),
which touched nothing in this repo. I did not push: that is an outward-facing
action on a repo that is not mine to publish from.

**Action:** `git push && git push --tags` from `~/taskman`. Until then the
install line in `tasks/262` is false for anyone not on this machine, and the
proxy keeps serving v0.3.0. Worth re-checking that
`go install github.com/freeeve/taskman@v0.4.0` resolves afterwards -- the proxy
caches a failed lookup for a while.

## Adoption

None for taskman. libcat is on the new binary and its ledger now carries one
deferred task.
