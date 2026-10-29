package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

var storageTransferName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func normalizeStorageTransfers(raw json.RawMessage, owner string) (*extensions.StorageTransfers, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var declarations extensions.StorageTransfers
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&declarations); err != nil {
		return nil, fmt.Errorf("storageTransfers: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("storageTransfers must be one object")
	}
	for direction, items := range map[string][]extensions.StorageTransferDeclaration{"exports": declarations.Exports, "imports": declarations.Imports} {
		if len(items) > 16 {
			return nil, fmt.Errorf("storageTransfers.%s exceeds 16 declarations", direction)
		}
		seen := map[string]bool{}
		for _, item := range items {
			if !storageTransferName.MatchString(item.ID) || !storageTransferName.MatchString(item.Plugin) || item.Plugin == owner || !storageTransferName.MatchString(item.Domain) || item.Scope != "user" || item.KeyPrefix == "" || pluginsettings.ValidateStateKey(item.KeyPrefix) != nil {
				return nil, fmt.Errorf("invalid storageTransfers.%s declaration %q", direction, item.ID)
			}
			if seen[item.ID] {
				return nil, fmt.Errorf("duplicate storage transfer id %q", item.ID)
			}
			seen[item.ID] = true
		}
		for i, a := range items {
			for _, b := range items[i+1:] {
				if a.Domain == b.Domain && (strings.HasPrefix(a.KeyPrefix, b.KeyPrefix) || strings.HasPrefix(b.KeyPrefix, a.KeyPrefix)) {
					return nil, fmt.Errorf("overlapping storage transfer prefixes")
				}
			}
		}
	}
	return &declarations, nil
}
