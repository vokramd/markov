# CC-71 staged signing hashes must be bound to the approved candidate

Fixed 2026-11-18 in site commit dc88afb. The signing stage previously checked package bytes against preparation-state hashes without explicitly requiring that map to equal the independently revalidated candidate inventory. A modified stage could therefore substitute a different hash map. The signer now derives the exact native-artifact/hash set from the verified candidate and checks equality, including the RPM request inventory, before any key operation.

Verification: `test_mutated_staged_hashes_cannot_override_approved_candidate` rejects modified staged hashes before constructing a signer; full 93-test suite and complete seven-artifact local acceptance passed. No real keys, publication or known public package incident was involved.
