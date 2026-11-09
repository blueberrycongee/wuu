package enginecatalog

import "errors"

// ErrCatalogUnsupported means a successful native handshake did not expose
// model discovery. It is distinct from a catalog with no available models.
var ErrCatalogUnsupported = errors.New("engine does not expose a model catalog")

// CatalogStatus describes the provenance of the currently displayed list.
func CatalogStatus(count int, err error) string {
	if errors.Is(err, ErrCatalogUnsupported) {
		return "unsupported"
	}
	if err != nil {
		return "error"
	}
	if count == 0 {
		return "empty"
	}
	return "ready"
}
