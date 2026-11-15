# CC-71 quickstart omitted by Ubuntu slim container policy

Discovered during local package pilot, before publication. Debian installed the example under `/usr/share/doc/markov`; Ubuntu 24.04's image excludes documentation paths, so native package install succeeded but copying the quickstart failed.

Fix: put the runnable example in `/usr/share/markov/examples/quickstart.yaml` in both nFPM and Arch recipes and the candidate container. Update smoke tests/install draft. README/license documentation remains in conventional documentation/license paths.

Verification: both package versions rebuilt; refreshed Ubuntu matrix results recorded in the phase-0/pilot note. No runtime engine behavior changed. This is a first-party packaging issue, not an Ubuntu defect.
