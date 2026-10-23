# OpenShell setup on the dev workstation (2026-10-23)

Fedora 44, podman 5.8.9. NVIDIA publishes no dnf repository; the COPR
`maxamillion/nvidia-openshell` stops at 0.0.85 (before the 0.1.0 `--from` change). Installed
the v0.1.5 release RPMs, checked against the release's sha256 files (they are unsigned); copies
are in `../openshell.rpms/`:

    sudo dnf install openshell-0.1.5-1.fc44.x86_64.rpm openshell-gateway-0.1.5-1.fc44.x86_64.rpm
    systemctl --user enable --now podman.socket openshell-gateway.service
    openshell gateway add https://127.0.0.1:17670 --local --name openshell

The RPM does not register the gateway with the CLI (install.sh does); without `gateway add`
every command fails with "No active gateway". The gateway auto-detects the Podman driver
(rootless, pasta networking) and creates the `openshell` podman network. Logs:
`journalctl --user -u openshell-gateway`. Upgrade: rerun install.sh or install the next
release's RPMs the same way.
