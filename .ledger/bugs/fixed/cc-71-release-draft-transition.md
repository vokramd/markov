# Validate Release identity and refresh URLs across draft publication

The v0.2.2 finalization guard observed an `untagged-…` draft association after
the notes update. It stopped before publishing an incorrectly named Release.
The existing annotated `v0.2.2` tag object and source commit were checked by
GET, then the draft was explicitly rebound with tag, commit, body and draft
state. A subsequent GET verified the association before publication. Include
the approved tag/commit in the publication PATCH as well; never infer tag
identity from the Release title alone or move an existing tag to bypass a guard.

After publication, the in-memory asset list still held draft-era download URLs.
The anonymous-download prefix guard stopped again. Refetch the asset list after
the transition, recheck names/digests/sizes, and then use its public URLs.
The Release was already public at this point; GET confirmed that state and the
continuation did not publish it again. All 28 anonymous downloads subsequently
matched their signed expected hashes and public-only GPG verification passed.

These corrections were applied to the private operator finalization helper in
`~/.cache/cc71/v0.2.2/stable/publication/`. They do not alter package bytes.
Preserved stage/transition receipts and failed logs document the fail-closed
checks. Future release automation must explicitly model this state transition
and GET after any ambiguous result instead of automatically retrying mutation.
