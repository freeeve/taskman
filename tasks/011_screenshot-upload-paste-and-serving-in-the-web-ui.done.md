# 011 -- screenshot upload paste and serving in the web ui

Opened 2026-07-10.

## Outcome

Multipart upload (10MB cap, sniffed to png/jpeg/gif/webp) into
<project>/screenshots/<NNN>/ (bare number, rename-stable), dated link
section appended to the task body, image+body in one commit; /shots/
serving with per-segment validation; rendered html rewrites
../screenshots/ links through /shots/ so images display inline; dialog
paste/drop upload. TestScreenshots covers roundtrip, traversal 404s,
non-image rejection, same-second name collisions, unknown task.
