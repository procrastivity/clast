package pi

// Test-only exports: the pi tests live in package pi_test because they
// drive the public capture verb, and internal/verbs/capture reaches the
// production registry, which imports this package (an in-package test
// importing it would be an import cycle). These aliases expose the few
// unexported pieces the tests pin directly.

type Header = header

var (
	ReadHeader     = readHeader
	ReadHeaderFile = readHeaderFile
	ErrSilentSkip  = errSilentSkip
)

const MaxLineSize = maxLineSize
