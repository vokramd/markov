# v0.2.1 tag, signing and publication

Human approved tag creation after [snapshot review](../../notes/release-v0.2.1-review.md).
Annotated tag v0.2.1 was pushed and remote peel verified at fcb6ce0. Tag object:
`d25141eb95b144657c4396e307228a0942d5653d`.
Clean-tag Actions run: https://github.com/vokramd/markov/actions/runs/44109044662
Candidate/native Arm verification and unsigned preparation passed. Three stable
native families and both exact OCI platforms passed. The signed release remains
pending real-key authorization; see [stable review](../../notes/release-v0.2.1-stable.md).
Real-key signing and publication still require separate approval.
Approved source: fcb6ce06de57025f4a21ac9d10a11814fff78381, tag v0.2.1.

Follow the existing clean-tag candidate/native Arm and host-signing runbooks.
Do not reuse or relabel the 0.2.1-dev snapshot as stable. Signing requires its own
approval/current verified key backup; publishing Release/GHCR/native repos/site
requires separate outward approval. Keep all versions and existing native URL
contract. Phase 2 and official-distro admission remain deferred.
