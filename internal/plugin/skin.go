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
	ID   string `json:"id"`
	Name string `json:"name"`
	// Placement selects where the artwork renders: "frame" decorates the window
	// frame only (default), "canvas" also places it behind the main pane.
	Placement string `json:"placement,omitempty"`
	// UserImage lets the user's Background image replace the skin's artwork,
	// processed with the host treatment for the skin's placement.
	UserImage bool            `json:"userImage,omitempty"`
	Light     SkinVariantSpec `json:"light"`
	Dark      SkinVariantSpec `json:"dark"`
}

type SkinVariantSpec struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Image      string `json:"image,omitempty"`
	Position   string `json:"position,omitempty"`
	Overlay    string `json:"overlay,omitempty"`
	Hover      string `json:"hover,omitempty"`
	Active     string `json:"active,omitempty"`
	// Veil is the opacity of the theme canvas laid over canvas artwork for
	// translucent destinations, in [0, 1]. Requires placement "canvas".
	Veil *float64 `json:"veil,omitempty"`
	// Dots renders the canvas artwork as a screen of solid cells, one sampled
	// color per cell. Requires placement "canvas".
	Dots *SkinDotsSpec `json:"dots,omitempty"`
}

type SkinDotsSpec struct {
	// Pitch is the dot grid spacing in CSS px, in [2, 32].
	Pitch float64 `json:"pitch"`
	// Size is the cell diameter or side in CSS px, in [0.5, pitch].
	Size float64 `json:"size"`
	// Shape is "round" (default) or "square".
	Shape string `json:"shape,omitempty"`
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
		if err := validateObjectFields(field, item, "id", "name", "placement", "userImage", "light", "dark"); err != nil {
			return nil, err
		}
		var skin SkinSpec
		if err := json.Unmarshal(item, &skin); err != nil {
			return nil, fmt.Errorf("%s: %w (placement must be a string, userImage a boolean)", field, err)
		}
		skin.ID, skin.Name = strings.TrimSpace(skin.ID), strings.TrimSpace(skin.Name)
		if !validCommandID(skin.ID) || seen[skin.ID] || skin.Name == "" {
			return nil, fmt.Errorf("%s requires a unique plugin-local id and name", field)
		}
		seen[skin.ID] = true
		switch skin.Placement {
		case "", "frame", "canvas":
		default:
			return nil, fmt.Errorf("%s.placement must be frame or canvas", field)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, err
		}
		for mode, variant := range map[string]*SkinVariantSpec{"light": &skin.Light, "dark": &skin.Dark} {
			if err := validateObjectFields(field+"."+mode, fields[mode], "background", "foreground", "image", "position", "overlay", "hover", "active", "veil", "dots"); err != nil {
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
			variantField := field + "." + mode
			if skin.Placement != "canvas" && (variant.Veil != nil || variant.Dots != nil) {
				return nil, fmt.Errorf("%s.veil and dots require canvas placement", variantField)
			}
			if variant.Veil != nil && (*variant.Veil < 0 || *variant.Veil > 1) {
				return nil, fmt.Errorf("%s.veil must be a number between 0 and 1", variantField)
			}
			var variantFields map[string]json.RawMessage
			if err := json.Unmarshal(fields[mode], &variantFields); err != nil {
				return nil, err
			}
			if raw, ok := variantFields["dots"]; ok {
				if err := validateObjectFields(variantField+".dots", raw, "pitch", "size", "shape"); err != nil {
					return nil, err
				}
				var dotFields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &dotFields); err != nil {
					return nil, fmt.Errorf("%s.dots must be an object", variantField)
				}
				if _, ok := dotFields["pitch"]; !ok {
					return nil, fmt.Errorf("%s.dots requires pitch", variantField)
				}
				if _, ok := dotFields["size"]; !ok {
					return nil, fmt.Errorf("%s.dots requires size", variantField)
				}
				dots := variant.Dots
				if dots == nil || dots.Pitch < 2 || dots.Pitch > 32 {
					return nil, fmt.Errorf("%s.dots.pitch must be a number between 2 and 32", variantField)
				}
				if dots.Size < 0.5 || dots.Size > dots.Pitch {
					return nil, fmt.Errorf("%s.dots.size must be a number between 0.5 and dots.pitch", variantField)
				}
				switch dots.Shape {
				case "", "round", "square":
				default:
					return nil, fmt.Errorf("%s.dots.shape must be round or square", variantField)
				}
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
