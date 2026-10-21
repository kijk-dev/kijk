package viewer

import (
	"os"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// The test binary also acts as the fake gh and glab of the tests.
func TestMain(m *testing.M) {
	gittest.RunFakeTool()
	os.Exit(m.Run())
}
