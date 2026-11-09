package skills

import (
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

// FormatAvailable returns the model-facing available skills catalog. It
// follows opencode's shape: verbose catalogs are XML, compact catalogs are a
// short markdown list for the skill tool description.
func FormatAvailable(all []Skill, verbose bool) string {
	described := modelVisibleWithDescriptions(all)
	if len(described) == 0 {
		return "No skills are currently available."
	}
	if verbose {
		var b strings.Builder
		b.WriteString("<available_skills>\n")
		for _, skill := range described {
			b.WriteString("  <skill>\n")
			fmt.Fprintf(&b, "    <name>%s</name>\n", xmlText(skill.Name))
			fmt.Fprintf(&b, "    <description>%s</description>\n", xmlText(skill.Description))
			if loc := skillLocationRef(skill.Path); loc != "" {
				fmt.Fprintf(&b, "    <location>%s</location>\n", xmlText(loc))
			}
			b.WriteString("  </skill>\n")
		}
		b.WriteString("</available_skills>")
		return b.String()
	}

	var b strings.Builder
	b.WriteString("## Available Skills\n")
	for _, skill := range described {
		fmt.Fprintf(&b, "- **%s**: %s\n", skill.Name, skill.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

func modelVisibleWithDescriptions(all []Skill) []Skill {
	out := make([]Skill, 0, len(all))
	for _, skill := range all {
		if skill.DisableModelInvoke || strings.TrimSpace(skill.Description) == "" {
			continue
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func skillLocationRef(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	}
	return path
}

func xmlText(value string) string {
	return html.EscapeString(value)
}
