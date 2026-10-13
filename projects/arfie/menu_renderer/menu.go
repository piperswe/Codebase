package main

// These input types mirror ../menu_scraper/menu.schema.json. The scraper is a
// command package, so the renderer owns its decoding types without importing it.
type Result struct {
	SchemaVersion      string      `json:"schema_version"`
	Status             string      `json:"status"`
	Restaurant         *Restaurant `json:"restaurant"`
	SelectionRationale *string     `json:"selection_rationale"`
	ExtractedAt        string      `json:"extracted_at"`
	Menus              []Menu      `json:"menus"`
	Sources            []Source    `json:"sources"`
	Warnings           []string    `json:"warnings"`
}

type Restaurant struct {
	Name    string  `json:"name"`
	City    *string `json:"city"`
	Address *string `json:"address"`
	Website *string `json:"website"`
}

type Menu struct {
	Name         string    `json:"name"`
	Availability *string   `json:"availability"`
	Notes        []string  `json:"notes"`
	Sections     []Section `json:"sections"`
}

type Section struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Items       []Item  `json:"items"`
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

type Price struct {
	Label    *string `json:"label"`
	Text     string  `json:"text"`
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
}

type OptionGroup struct {
	Name          string       `json:"name"`
	MinSelections *int         `json:"min_selections"`
	MaxSelections *int         `json:"max_selections"`
	Options       []MenuOption `json:"options"`
}

type MenuOption struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Prices      []Price `json:"prices"`
}

type SourceRef struct {
	SourceID string `json:"source_id"`
	Page     *int   `json:"page"`
}

type Source struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	MediaType   string `json:"media_type"`
	RetrievedAt string `json:"retrieved_at"`
}
