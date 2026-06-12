package testsuite

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TempFilename(tb testing.TB, pattern string) (name string) {
	tb.Helper()

	assertions := assert.New(tb)

	file, err := os.CreateTemp(tb.TempDir(), pattern)
	if !assertions.NoError(err, "failed to create temporary file") {
		return ""
	}
	file.Close()
	tb.Cleanup(func() {
		os.Remove(file.Name())
	})

	return file.Name()
}

func TempDirectory(tb testing.TB, pattern string) (name string) {
	tb.Helper()

	assertions := assert.New(tb)

	name, err := os.MkdirTemp(tb.TempDir(), pattern)
	if !assertions.NoError(err, "failed to create temporary file") {
		return ""
	}
	tb.Cleanup(func() {
		os.RemoveAll(name)
	})

	return name
}
