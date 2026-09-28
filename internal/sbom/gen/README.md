# Build-time SBOM drop point

`make sbom` writes `sbom.cdx.json.gz` here — the CycloneDX 1.6 document
merged from the daemon's Go module graph and the Config UI's npm tree —
and the release workflow runs it before `go build`, so release binaries
embed it (`internal/sbom`). The archive is gitignored; a development
build without it answers 404 on `GET /api/v1/sbom`, which the Licenses
page reports as "this build carries no SBOM".
