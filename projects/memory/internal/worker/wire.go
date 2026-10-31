package worker

import (
	"codebase.bid/projects/memory/internal/model"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"encoding/json"
)

func page(x model.PageOptions) *p.PageOptions {
	return &p.PageOptions{Limit: uint32(x.Limit), Cursor: x.Cursor}
}
func options(x *p.PageOptions) model.PageOptions {
	if x == nil {
		return model.PageOptions{}
	}
	return model.PageOptions{Limit: int(x.Limit), Cursor: x.Cursor}
}
func meta(complete bool, next *string) *p.PageMeta {
	return &p.PageMeta{Complete: complete, NextCursor: next}
}
func value(v model.Value) *p.Value {
	out := &p.Value{Type: string(v.Type)}
	if v.Type == model.Boolean {
		out.Payload = &p.Value_Boolean{Boolean: v.Bool}
	} else {
		out.Payload = &p.Value_Exact{Exact: v.Text}
	}
	return out
}
func modelValue(v *p.Value) (model.Value, error) {
	if v == nil {
		return model.Value{}, model.Invalid("typed value")
	}
	out := model.Value{Type: model.ValueType(v.Type)}
	switch x := v.Payload.(type) {
	case *p.Value_Exact:
		if out.Type == model.Boolean {
			return out, model.Invalid("boolean payload")
		}
		out.Text = x.Exact
	case *p.Value_Boolean:
		if out.Type != model.Boolean {
			return out, model.Invalid("typed payload")
		}
		out.Bool = x.Boolean
	default:
		return out, model.Invalid("value payload")
	}
	if out.Type == model.Ref && (model.ValidName(out.Text)) {
		return out, nil
	}
	return out.Canonical()
}
func entity(v model.Entity) *p.Entity {
	out := &p.Entity{Id: v.ID, Ident: v.Ident, Kind: v.Kind, Revision: v.Revision, Archived: v.Archived}
	for _, f := range v.Facts {
		pf := &p.Fact{Id: f.ID, Entity: f.Entity, Attribute: f.Attribute, Value: value(f.Value), Revision: f.Revision, Virtual: f.Virtual}
		for _, e := range f.Evidence {
			pf.Evidence = append(pf.Evidence, &p.Evidence{Source: e.Source, Confidence: e.Confidence, Method: e.Method, ObservedAt: e.ObservedAt})
		}
		out.Facts = append(out.Facts, pf)
	}
	return out
}
func modelEntity(v *p.Entity) (model.Entity, error) {
	out := model.Entity{ID: v.Id, Ident: v.Ident, Kind: v.Kind, Revision: v.Revision, Archived: v.Archived, Facts: []model.Fact{}}
	for _, f := range v.Facts {
		x, e := modelValue(f.Value)
		if e != nil {
			return out, e
		}
		mf := model.Fact{ID: f.Id, Entity: f.Entity, Attribute: f.Attribute, Value: x, Revision: f.Revision, Virtual: f.Virtual, Evidence: []model.Evidence{}}
		for _, ev := range f.Evidence {
			mf.Evidence = append(mf.Evidence, model.Evidence{Source: ev.Source, Confidence: ev.Confidence, Method: ev.Method, ObservedAt: ev.ObservedAt})
		}
		out.Facts = append(out.Facts, mf)
	}
	return out, nil
}
func entities(v []model.Entity, complete bool, next *string) *p.EntityPage {
	out := &p.EntityPage{Meta: meta(complete, next)}
	for _, e := range v {
		out.Items = append(out.Items, entity(e))
	}
	return out
}
func definition(d model.Definition) *p.Definition {
	return &p.Definition{Id: d.ID, Ident: d.Ident, Kind: d.Kind, Description: d.Description, ValueType: string(d.ValueType), Cardinality: d.Cardinality, AppliesTo: d.AppliesTo, ReferenceTypes: d.ReferenceTypes, Unit: d.Unit, Status: d.Status, Replacement: d.Replacement, Examples: d.Examples, NotThis: d.NotThis, Aliases: d.Aliases, SchemaRevision: d.SchemaRevision, Protected: d.Protected}
}
func modelDefinition(d *p.Definition) model.Definition {
	return model.Definition{ID: d.Id, Ident: d.Ident, Kind: d.Kind, Description: d.Description, ValueType: model.ValueType(d.ValueType), Cardinality: d.Cardinality, AppliesTo: d.AppliesTo, ReferenceTypes: d.ReferenceTypes, Unit: d.Unit, Status: d.Status, Replacement: d.Replacement, Examples: d.Examples, NotThis: d.NotThis, Aliases: d.Aliases, SchemaRevision: d.SchemaRevision, Protected: d.Protected}
}
func predicate(v model.Predicate) *p.Predicate {
	out := &p.Predicate{Attribute: v.Attribute, Op: v.Op}
	if v.Value != nil {
		out.Value = value(*v.Value)
	}
	for _, x := range v.Values {
		out.Values = append(out.Values, value(x))
	}
	for _, x := range v.All {
		out.All = append(out.All, predicate(x))
	}
	for _, x := range v.Any {
		out.Any = append(out.Any, predicate(x))
	}
	for _, x := range v.Path {
		out.Path = append(out.Path, &p.PathStep{Attribute: x.Attribute, Direction: x.Direction})
	}
	return out
}
func modelPredicate(v *p.Predicate, depth int) (model.Predicate, error) {
	var out model.Predicate
	if v == nil || depth > 8 {
		return out, model.Invalid("predicate")
	}
	out.Attribute = v.Attribute
	out.Op = v.Op
	if v.Value != nil {
		x, e := modelValue(v.Value)
		if e != nil {
			return out, e
		}
		out.Value = &x
	}
	for _, x := range v.Values {
		v, e := modelValue(x)
		if e != nil {
			return out, e
		}
		out.Values = append(out.Values, v)
	}
	for _, x := range v.All {
		v, e := modelPredicate(x, depth+1)
		if e != nil {
			return out, e
		}
		out.All = append(out.All, v)
	}
	for _, x := range v.Any {
		v, e := modelPredicate(x, depth+1)
		if e != nil {
			return out, e
		}
		out.Any = append(out.Any, v)
	}
	for _, x := range v.Path {
		out.Path = append(out.Path, model.PathStep{Attribute: x.Attribute, Direction: x.Direction})
	}
	return out, nil
}
func pattern(v model.Pattern) (*p.PullPattern, error) {
	out := &p.PullPattern{Limit: uint32(v.Limit)}
	for a, raw := range v.Select {
		s := &p.Selection{Attribute: a}
		if string(raw) == "true" {
			s.Projection = &p.Selection_Scalar{Scalar: true}
		} else {
			var child model.Pattern
			if e := model.Decode(raw, &child); e != nil {
				return nil, e
			}
			sub, e := pattern(child)
			if e != nil {
				return nil, e
			}
			s.Projection = &p.Selection_Nested{Nested: sub}
		}
		out.Select = append(out.Select, s)
	}
	for _, in := range v.Incoming {
		sub, e := pattern(model.Pattern{Select: in.Select})
		if e != nil {
			return nil, e
		}
		out.Incoming = append(out.Incoming, &p.Incoming{Attribute: in.Attribute, Select: sub.Select, Limit: uint32(in.Limit)})
	}
	return out, nil
}
func modelPattern(v *p.PullPattern, depth int) (model.Pattern, error) {
	out := model.Pattern{Select: map[string]json.RawMessage{}}
	if v == nil || depth > 5 || len(v.Select) > 100 || len(v.Incoming) > 20 {
		return out, model.Limit("Invalid pull pattern")
	}
	out.Limit = int(v.Limit)
	for _, s := range v.Select {
		if _, exists := out.Select[s.Attribute]; exists {
			return out, model.Invalid("duplicate projection")
		}
		switch x := s.Projection.(type) {
		case *p.Selection_Scalar:
			if !x.Scalar {
				return out, model.Invalid("projection")
			}
			out.Select[s.Attribute] = json.RawMessage(`true`)
		case *p.Selection_Nested:
			child, e := modelPattern(x.Nested, depth+1)
			if e != nil {
				return out, e
			}
			out.Select[s.Attribute], _ = json.Marshal(child)
		default:
			return out, model.Invalid("projection")
		}
	}
	for _, in := range v.Incoming {
		child, e := modelPattern(&p.PullPattern{Select: in.Select}, depth+1)
		if e != nil {
			return out, e
		}
		out.Incoming = append(out.Incoming, model.Incoming{Attribute: in.Attribute, Select: child.Select, Limit: int(in.Limit)})
	}
	return out, nil
}
func pull(v model.PullResult) *p.PullResult {
	out := &p.PullResult{Entity: entity(v.Entity), Stub: v.Stub}
	for a, e := range v.Edges {
		edge := &p.PullEdge{Attribute: a, Meta: meta(e.Complete, e.NextCursor)}
		for _, x := range e.Items {
			edge.Items = append(edge.Items, pull(x))
		}
		out.Edges = append(out.Edges, edge)
	}
	return out
}
func modelPull(v *p.PullResult) (model.PullResult, error) {
	e, err := modelEntity(v.Entity)
	out := model.PullResult{Entity: e, Stub: v.Stub, Edges: map[string]model.Edge{}}
	if err != nil {
		return out, err
	}
	for _, edge := range v.Edges {
		e := model.Edge{Items: []model.PullResult{}, Complete: edge.Meta.GetComplete(), NextCursor: edge.Meta.NextCursor}
		for _, x := range edge.Items {
			v, err := modelPull(x)
			if err != nil {
				return out, err
			}
			e.Items = append(e.Items, v)
		}
		out.Edges[edge.Attribute] = e
	}
	return out, nil
}
func responseData(v *p.GraphResponse) (any, error) {
	switch x := v.Result.(type) {
	case *p.GraphResponse_Error:
		return nil, &model.Error{Code: x.Error.Code, Message: x.Error.Message, Retryable: x.Error.Retryable}
	case *p.GraphResponse_Facts:
		converted, e := modelEntity(&p.Entity{Facts: x.Facts.Items})
		if e != nil {
			return nil, e
		}
		return model.Page[model.Fact]{Items: converted.Facts, Complete: x.Facts.Meta.GetComplete(), NextCursor: x.Facts.Meta.NextCursor}, nil
	case *p.GraphResponse_Entities:
		out := model.Page[model.Entity]{Items: []model.Entity{}, Complete: x.Entities.Meta.GetComplete(), NextCursor: x.Entities.Meta.NextCursor}
		for _, e := range x.Entities.Items {
			v, err := modelEntity(e)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, v)
		}
		return out, nil
	case *p.GraphResponse_Hits:
		out := model.Page[model.Hit]{Items: []model.Hit{}, Complete: x.Hits.Meta.GetComplete(), NextCursor: x.Hits.Meta.NextCursor}
		for _, h := range x.Hits.Items {
			out.Items = append(out.Items, model.Hit{ID: h.Id, Ident: h.Ident, Kind: h.Kind, Snippet: h.Snippet})
		}
		return out, nil
	case *p.GraphResponse_Definitions:
		out := model.Page[model.Definition]{Items: []model.Definition{}, Complete: x.Definitions.Meta.GetComplete(), NextCursor: x.Definitions.Meta.NextCursor}
		for _, d := range x.Definitions.Items {
			out.Items = append(out.Items, modelDefinition(d))
		}
		return out, nil
	case *p.GraphResponse_Pull:
		return modelPull(x.Pull)
	case *p.GraphResponse_History:
		out := model.Page[model.Event]{Items: []model.Event{}, Complete: x.History.Meta.GetComplete(), NextCursor: x.History.Meta.NextCursor}
		for _, e := range x.History.Items {
			out.Items = append(out.Items, model.Event{Revision: e.Revision, Ordinal: int(e.Ordinal), Kind: e.Kind, Entity: e.Entity, Payload: e.AuditJson, Actor: e.Actor, Client: e.Client, Reason: e.Reason, CommittedAt: e.CommittedAt})
		}
		return out, nil
	case *p.GraphResponse_Help:
		return map[string]string{"sdkVersion": x.Help.SdkVersion, "documentation": x.Help.Documentation}, nil
	default:
		return nil, model.E("INCOMPATIBLE_PROTOCOL", "Unexpected graph response")
	}
}
