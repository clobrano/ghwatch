package notify

import (
	"os"
	"testing"

	"github.com/clobrano/ghwatch/internal/model"
)

// TestMain pins the safe icon set, which the tests assert on; the default
// (fancy) set is tested on its own.
func TestMain(m *testing.M) {
	model.UseIcons("safe")
	os.Exit(m.Run())
}
