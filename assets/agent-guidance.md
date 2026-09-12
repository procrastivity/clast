clast owns the journal; you own nothing inside it. Read journal state
through clast verbs, never by opening files under the journal
directory — record formats are internal and the tree layout can
change. Every verb writes only its payload to stdout; pass `--json`
for one parseable value, and read errors from stderr or the `--json`
error envelope, never from the exit code alone. Files clast installed
into a harness are generated and stamped — never hand-edit them;
re-run `clast install <harness>` or ask `clast doctor` what drifted.
