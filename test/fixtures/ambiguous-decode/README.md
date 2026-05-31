# ambiguous-decode fixture

This fixture cannot live entirely on disk under `test/fixtures/` because the
ambiguity it tests requires real directory entries at `/tmp/clast/...` and
`/tmp/clast-foo/...`. The decoder probes the filesystem to resolve a segment
that has multiple valid decodings.

`test/test-decode.sh` materializes the candidates at test time:

```
mkdir -p /tmp/clast/foo/bar/baz       # the "intended" decoding
mkdir -p /tmp/clast-foo/bar/baz       # a decoy that also satisfies the segment
git init /tmp/clast/foo/bar/baz       # a git-repo signal favoring the intended one
```

The segment under test is `-tmp-clast-foo-bar-baz`, which has three plausible
decodings:

- `/tmp/clast/foo/bar/baz`
- `/tmp/clast/foo-bar/baz`
- `/tmp/clast-foo/bar/baz`

With the setup above, two candidates exist on disk, and only one is a git
repo — the decoder should pick `/tmp/clast/foo/bar/baz`. The test cleans up
both paths in teardown.

This README is the *only* thing committed under
`test/fixtures/ambiguous-decode/`.
