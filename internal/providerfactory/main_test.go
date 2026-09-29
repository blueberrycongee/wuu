package providerfactory

import (
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
