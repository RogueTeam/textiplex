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
	if !assertions.Nil(err, "failed to create temporary file") {
		return ""
	}
	file.Close()
	tb.Cleanup(func() {
		os.Remove(file.Name())
	})

	return file.Name()
}
