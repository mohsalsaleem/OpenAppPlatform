package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

func TestOpStoreContract(t *testing.T) {
	contract.OpStore(t, func(t *testing.T) ports.OpStore { return open(t, filepath.Join(t.TempDir(), "oap.db")) })
}
