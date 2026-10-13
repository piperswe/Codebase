package main

import (
	_ "embed"
	"fmt"
	"net/url"
	"strings"
)

// Only this embedded, repository-owned stylesheet is rendered as raw HTML.
//
//go:embed menu.css
var menuCSS string

func hasText(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func pageTitle(result Result) string {
	if result.Restaurant != nil && strings.TrimSpace(result.Restaurant.Name) != "" {
		return result.Restaurant.Name
	}
	return "Restaurant menu"
}

func httpURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Hostname() != "" && (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https"))
}

func selectionLimits(group OptionGroup) string {
	switch {
	case group.MinSelections != nil && group.MaxSelections != nil:
		if *group.MinSelections == *group.MaxSelections {
			return fmt.Sprintf("Choose exactly %d", *group.MinSelections)
		}
		return fmt.Sprintf("Choose %d–%d", *group.MinSelections, *group.MaxSelections)
	case group.MinSelections != nil:
		return fmt.Sprintf("Choose at least %d", *group.MinSelections)
	case group.MaxSelections != nil:
		return fmt.Sprintf("Choose up to %d", *group.MaxSelections)
	default:
		return ""
	}
}

func sourceAnchors(sources []Source) map[string]string {
	anchors := make(map[string]string, len(sources))
	for i, source := range sources {
		if _, exists := anchors[source.ID]; !exists {
			anchors[source.ID] = fmt.Sprintf("source-%d", i+1)
		}
	}
	return anchors
}

func statusText(status string) string {
	switch status {
	case "partial":
		return "This menu extraction is incomplete. Review the warnings below."
	case "not_found":
		return "No menu was found."
	case "error":
		return "The menu could not be fully extracted."
	default:
		return ""
	}
}
