package model

import (
	"context"
	"encoding/json"
)

const SDKVersion = "1.0"
const RPCVersion = "memory.worker.v1"

type Principal struct {
	Actor  string   `json:"actor"`
	Client string   `json:"client"`
	Scopes []string `json:"scopes"`
	Spaces []string `json:"spaces"`
}

func (p Principal) Allow(space, scope string) bool {
	s, g := false, false
	for _, x := range p.Scopes {
		s = s || x == scope
	}
	for _, x := range p.Spaces {
		g = g || x == space
	}
	return s && g && p.Actor != ""
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

type Entity struct {
	ID       string  `json:"id"`
	Ident    *string `json:"ident"`
	Kind     string  `json:"kind"`
	Revision string  `json:"revision"`
	Archived bool    `json:"archived"`
	Facts    []Fact  `json:"facts"`
}
type Evidence struct {
	Source     string   `json:"source"`
	Confidence *float64 `json:"confidence"`
	Method     string   `json:"method,omitempty"`
	ObservedAt *string  `json:"observedAt,omitempty"`
}
type Fact struct {
	ID        string     `json:"id"`
	Entity    string     `json:"entity"`
	Attribute string     `json:"attribute"`
	Value     Value      `json:"value"`
	Revision  string     `json:"revision"`
	Virtual   bool       `json:"virtual"`
	Evidence  []Evidence `json:"evidence"`
}
type Definition struct {
	ID             string    `json:"id,omitempty"`
	Ident          string    `json:"ident"`
	Kind           string    `json:"kind"`
	Description    string    `json:"description"`
	ValueType      ValueType `json:"valueType,omitempty"`
	Cardinality    string    `json:"cardinality,omitempty"`
	AppliesTo      []string  `json:"appliesTo"`
	ReferenceTypes []string  `json:"referenceTypes"`
	Unit           string    `json:"unit,omitempty"`
	Status         string    `json:"status"`
	Replacement    string    `json:"replacement,omitempty"`
	Examples       []string  `json:"examples"`
	NotThis        []string  `json:"notThis"`
	Aliases        []string  `json:"aliases"`
	SchemaRevision string    `json:"schemaRevision,omitempty"`
	Protected      bool      `json:"protected"`
}
type Source struct {
	Origin      string  `json:"origin"`
	URI         string  `json:"uri,omitempty"`
	Description string  `json:"description"`
	Locator     string  `json:"locator,omitempty"`
	ObservedAt  *string `json:"observedAt,omitempty"`
}
type EntityRef struct {
	ID     string
	TempID string
}

func (r EntityRef) MarshalJSON() ([]byte, error) {
	if r.TempID != "" {
		return json.Marshal(struct {
			TempID string `json:"tempId"`
		}{r.TempID})
	}
	return json.Marshal(r.ID)
}
func (r *EntityRef) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.ID)
	}
	var t struct {
		TempID string `json:"tempId"`
	}
	if err := Decode(b, &t); err != nil {
		return err
	}
	r.TempID = t.TempID
	return nil
}

type Operation struct {
	Op               string     `json:"op"`
	TempID           string     `json:"tempId,omitempty"`
	Ident            string     `json:"ident,omitempty"`
	Types            []string   `json:"types,omitempty"`
	Entity           EntityRef  `json:"entity,omitzero"`
	Attribute        string     `json:"attribute,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	Value            *Value     `json:"value,omitempty"`
	Evidence         []Evidence `json:"evidence,omitempty"`
	Source           *Source    `json:"source,omitempty"`
	ExpectedRevision string     `json:"expectedRevision,omitempty"`
	ExpectedFact     string     `json:"expectedFact,omitempty"`
	Absent           bool       `json:"absent,omitempty"`
	Name             string     `json:"name,omitempty"`
}
type Transaction struct {
	Space                  string            `json:"space"`
	IdempotencyKey         string            `json:"idempotencyKey"`
	ExpectedSchemaRevision string            `json:"expectedSchemaRevision"`
	Reason                 string            `json:"reason"`
	Preconditions          map[string]string `json:"preconditions,omitempty"`
	Operations             []Operation       `json:"operations"`
}
type Commit struct {
	Revision       string            `json:"revision"`
	SchemaRevision string            `json:"schemaRevision"`
	TempIDs        map[string]string `json:"tempIds"`
	Applied        int               `json:"applied"`
	Noop           int               `json:"noop"`
	Warnings       []string          `json:"warnings"`
}
type Proposal struct {
	Space      string     `json:"space"`
	Definition Definition `json:"definition"`
}
type Apply struct {
	Space                  string `json:"space"`
	Proposal               string `json:"proposal"`
	ExpectedSchemaRevision string `json:"expectedSchemaRevision"`
	Action                 string `json:"action"`
	Target                 string `json:"target,omitempty"`
	Rationale              string `json:"rationale"`
}
type PageOptions struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	Complete   bool    `json:"complete"`
	NextCursor *string `json:"nextCursor"`
}
type Search struct {
	Space string   `json:"space"`
	Query string   `json:"query,omitempty"`
	Kind  string   `json:"kind,omitempty"`
	Types []string `json:"types,omitempty"`
	PageOptions
}
type Hit struct {
	ID      string  `json:"id"`
	Ident   *string `json:"ident"`
	Kind    string  `json:"kind"`
	Snippet string  `json:"snippet"`
}
type PathStep struct {
	Attribute string `json:"attribute"`
	Direction string `json:"direction"`
}
type Predicate struct {
	All       []Predicate `json:"all,omitempty"`
	Any       []Predicate `json:"any,omitempty"`
	Attribute string      `json:"attribute,omitempty"`
	Op        string      `json:"op,omitempty"`
	Value     *Value      `json:"value,omitempty"`
	Values    []Value     `json:"values,omitempty"`
	Path      []PathStep  `json:"path,omitempty"`
}
type Find struct {
	Space  string    `json:"space"`
	Where  Predicate `json:"where"`
	Select []string  `json:"select,omitempty"`
	Types  []string  `json:"types,omitempty"`
	Query  string    `json:"query,omitempty"`
	PageOptions
}
type Get struct {
	Space      string   `json:"space"`
	Entities   []string `json:"entities"`
	Attributes []string `json:"attributes,omitempty"`
}
type Pattern struct {
	Select   map[string]json.RawMessage `json:"select"`
	Incoming []Incoming                 `json:"incoming,omitempty"`
	Limit    int                        `json:"limit,omitempty"`
}
type Incoming struct {
	Attribute string                     `json:"attribute"`
	Select    map[string]json.RawMessage `json:"select"`
	Limit     int                        `json:"limit,omitempty"`
}
type Pull struct {
	Space   string  `json:"space"`
	Entity  string  `json:"entity"`
	Pattern Pattern `json:"pattern"`
}
type Edge struct {
	Items      []PullResult `json:"items"`
	Complete   bool         `json:"complete"`
	NextCursor *string      `json:"nextCursor"`
}
type PullResult struct {
	Entity Entity          `json:"entity"`
	Edges  map[string]Edge `json:"edges"`
	Stub   bool            `json:"stub"`
}
type History struct {
	Space  string `json:"space"`
	Entity string `json:"entity,omitempty"`
	PageOptions
}
type Event struct {
	Revision    string          `json:"revision"`
	Ordinal     int             `json:"ordinal"`
	Kind        string          `json:"kind"`
	Entity      string          `json:"entity"`
	Payload     json.RawMessage `json:"payload"`
	Actor       string          `json:"actor"`
	Client      string          `json:"client"`
	Reason      string          `json:"reason"`
	CommittedAt string          `json:"committedAt"`
}
type SchemaQuery struct {
	Space  string `json:"space"`
	Action string `json:"action"`
	Entity string `json:"entity,omitempty"`
	Query  string `json:"query,omitempty"`
	PageOptions
}
type Code struct {
	Space     string          `json:"space"`
	Source    string          `json:"source"`
	Input     json.RawMessage `json:"input"`
	TimeoutMS int             `json:"timeoutMs,omitempty"`
}
type Meta struct {
	RequestID      string  `json:"requestId"`
	SpaceRevision  string  `json:"spaceRevision"`
	SchemaRevision string  `json:"schemaRevision"`
	Complete       bool    `json:"complete"`
	NextCursor     *string `json:"nextCursor"`
}
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
	Meta  Meta   `json:"meta"`
}

// Schema arrays are always arrays on the wire, including empty definitions.
func (d Definition) MarshalJSON() ([]byte, error) {
	type wire Definition
	x := wire(d)
	if x.AppliesTo == nil {
		x.AppliesTo = []string{}
	}
	if x.ReferenceTypes == nil {
		x.ReferenceTypes = []string{}
	}
	if x.Examples == nil {
		x.Examples = []string{}
	}
	if x.NotThis == nil {
		x.NotThis = []string{}
	}
	if x.Aliases == nil {
		x.Aliases = []string{}
	}
	return json.Marshal(x)
}
