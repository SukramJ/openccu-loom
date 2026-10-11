# Download statistics

Daily samples of the public usage counters, appended by the
`download-stats` workflow. `downloads.ndjson` holds one JSON object per
line; the row shapes are documented in
`script/release_download_snapshot.py` on `main`. Never edit by hand —
the file is append-only and the workflow is its only writer.
