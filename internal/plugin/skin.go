package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SkinSpec decorates window chrome independently of content themes.
type SkinSpec struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Light SkinVariantSpec `json:"light"`
	Dark  SkinVariantSpec `json:"dark"`
}

type SkinVariantSpec struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Image      string `json:"image,omitempty"`
	Position   string `json:"position,omitempty"`
	Overlay    string `json:"overlay,omitempty"`
	Hover      string `json:"hover,omitempty"`
	Active     string `json:"active,omitempty"`
}

var skinColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)

func normalizeSkins(root string, raw json.RawMessage) ([]SkinSpec, error) {
	items, err := decodeContributionArray("contributes.skins", raw)
	if err != nil {
		return nil, err
	}
	var skins []SkinSpec
	seen := make(map[string]bool)
	for index, item := range items {
		field := fmt.Sprintf("contributes.skins[%d]", index)
		if err := validateObjectFields(field, item, "id", "name", "light", "dark"); err != nil {
			return nil, err
		}
		var skin SkinSpec
		if err := json.Unmarshal(item, &skin); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		skin.ID, skin.Name = strings.TrimSpace(skin.ID), strings.TrimSpace(skin.Name)
		if !validCommandID(skin.ID) || seen[skin.ID] || skin.Name == "" {
			return nil, fmt.Errorf("%s requires a unique plugin-local id and name", field)
		}
		seen[skin.ID] = true
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, err
		}
		for mode, variant := range map[string]*SkinVariantSpec{"light": &skin.Light, "dark": &skin.Dark} {
			if err := validateObjectFields(field+"."+mode, fields[mode], "background", "foreground", "image", "position", "overlay", "hover", "active"); err != nil {
				return nil, err
			}
			for name, value := range map[string]string{"background": variant.Background, "foreground": variant.Foreground, "overlay": variant.Overlay, "hover": variant.Hover, "active": variant.Active} {
				if value == "" && name != "background" && name != "foreground" {
					continue
				}
				if !skinColor.MatchString(value) || ((name == "background" || name == "foreground") && len(value) != 7) {
					return nil, fmt.Errorf("%s.%s.%s must be a hex color (background and foreground must be opaque)", field, mode, name)
				}
			}
			switch variant.Position {
			case "", "center", "top", "bottom", "left", "right":
			default:
				return nil, fmt.Errorf("%s.%s.position must be center, top, bottom, left, or right", field, mode)
			}
			if variant.Image != "" {
				path, err := normalizeImageAssetPath(root, field+"."+mode+".image", variant.Image, 8<<20, true)
				if err != nil {
					return nil, err
				}
				variant.Image = path
			}
		}
		skins = append(skins, skin)
	}
	sort.Slice(skins, func(i, j int) bool { return skins[i].ID < skins[j].ID })
	return skins, nil
}
