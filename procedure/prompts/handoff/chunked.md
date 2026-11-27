## Chunked diff

The diff arrived sliced. Read `chunks/manifest.json` first, then every listed `chunks/chunk-NN.diff` end to end against its own `chunks/chunk-NN.toc.txt`. Keep `full.diff` for anchor pinning and drift detection; review from the chunks. An unread chunk is an unread change. Attest the chunks you read in top-level `chunks[]`, naming every manifest chunk; `coverage[]` is ignored here.
