package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codebase.bid/lib/go/fetchweb"
	"github.com/grafana/ai-sdk/schema"
)

//go:embed menu.schema.json
var menuSchema []byte

type Restaurant struct {
	Name    string  `json:"name"`
	City    *string `json:"city"`
	Address *string `json:"address"`
	Website *string `json:"website"`
}

type Price struct {
	Label    *string `json:"label"`
	Text     string  `json:"text"`
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
}

type MenuOption struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Prices      []Price `json:"prices"`
}

type OptionGroup struct {
	Name          string       `json:"name"`
	MinSelections *int         `json:"min_selections"`
	MaxSelections *int         `json:"max_selections"`
	Options       []MenuOption `json:"options"`
}

type SourceRef struct {
	SourceID string `json:"source_id"`
	Page     *int   `json:"page"`
}

type Item struct {
	Name          string        `json:"name"`
	Description   *string       `json:"description"`
	Prices        []Price       `json:"prices"`
	OptionGroups  []OptionGroup `json:"option_groups"`
	DietaryLabels []string      `json:"dietary_labels"`
	Allergens     []string      `json:"allergens"`
	Notes         []string      `json:"notes"`
	Sources       []SourceRef   `json:"sources"`
}

type Section struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Items       []Item  `json:"items"`
}

type Menu struct {
	Name         string    `json:"name"`
	Availability *string   `json:"availability"`
	Notes        []string  `json:"notes"`
	Sections     []Section `json:"sections"`
}

type Result struct {
	SchemaVersion      string            `json:"schema_version"`
	Status             string            `json:"status"`
	Restaurant         *Restaurant       `json:"restaurant"`
	SelectionRationale *string           `json:"selection_rationale"`
	ExtractedAt        string            `json:"extracted_at"`
	Menus              []Menu            `json:"menus"`
	Sources            []fetchweb.Source `json:"sources"`
	Warnings           []string          `json:"warnings"`
}

type Extraction struct {
	Menus    []Menu   `json:"menus"`
	Warnings []string `json:"warnings"`
}

func emptyResult() Result {
	return Result{SchemaVersion: "1", Status: "error", ExtractedAt: time.Now().UTC().Format(time.RFC3339), Menus: []Menu{}, Sources: []fetchweb.Source{}, Warnings: []string{}}
}

func extractionSchema() (schema.Schema, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(menuSchema, &root); err != nil {
		return schema.Schema{}, err
	}
	root["properties"] = json.RawMessage(`{"menus":{"type":"array","items":{"$ref":"#/$defs/menu"}},"warnings":{"type":"array","items":{"type":"string"}}}`)
	root["required"] = json.RawMessage(`["menus","warnings"]`)
	data, err := json.Marshal(root)
	if err != nil {
		return schema.Schema{}, err
	}
	return schema.SchemaFromJSON(data)
}

func itemCount(menus []Menu) int {
	n := 0
	for _, m := range menus {
		for _, s := range m.Sections {
			n += len(s.Items)
		}
	}
	return n
}

func validateResult(r Result) error {
	s, err := schema.SchemaFromJSON(menuSchema)
	if err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := s.Validate(b); err != nil {
		return err
	}
	if (r.Status == "ok" || r.Status == "partial") && (r.Restaurant == nil || itemCount(r.Menus) == 0) {
		return fmt.Errorf("successful result needs a restaurant and menu items")
	}
	known := map[string]fetchweb.Source{}
	for _, source := range r.Sources {
		if _, exists := known[source.ID]; exists {
			return fmt.Errorf("duplicate source ID %s", source.ID)
		}
		known[source.ID] = source
	}
	for _, menu := range r.Menus {
		for _, section := range menu.Sections {
			for _, item := range section.Items {
				if strings.TrimSpace(item.Name) == "" {
					return fmt.Errorf("empty item name")
				}
				for _, ref := range item.Sources {
					source, ok := known[ref.SourceID]
					if !ok {
						return fmt.Errorf("unknown source ID %s", ref.SourceID)
					}
					if ref.Page != nil && source.MediaType != "application/pdf" {
						return fmt.Errorf("page reference on non-PDF source %s", ref.SourceID)
					}
				}
				for _, group := range item.OptionGroups {
					if group.MinSelections != nil && group.MaxSelections != nil && *group.MinSelections > *group.MaxSelections {
						return fmt.Errorf("invalid option selection range")
					}
				}
			}
		}
	}
	return nil
}
