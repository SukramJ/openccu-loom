<!--
Home Assistant renders this file as the add-on changelog in its UI.
Keep entries condensed; the full history lives in the repository's
top-level CHANGELOG.md. Newest version first. The "Unreleased" header
is replaced by the version number in the release commit.
-->

# Unreleased

Initial release of **OpenCCU-Loom Remote** — an ingress proxy that
brings the Config UI of one or more remote OpenCCU-Loom instances into
the Home Assistant sidebar (no local daemon required).

- Multiple instances behind one panel: overview page with live status
  tiles (health, version), each instance under its own path.
- Optional per-instance API token: HA admins land in the remote UI
  without a second login; without a token the remote login page is
  proxied through.
- `http://` and `https://` upstreams; `tls_insecure` flag per instance
  for self-signed certificates.
