package worker

import (
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func noUnknown(m protoreflect.Message, depth int) bool {
	if depth > 32 || len(m.GetUnknown()) > 0 {
		return false
	}
	ok := true
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Kind() != protoreflect.MessageKind {
			return true
		}
		if f.IsList() {
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				if !noUnknown(l.Get(i).Message(), depth+1) {
					ok = false
					return false
				}
			}
		} else if !noUnknown(v.Message(), depth+1) {
			ok = false
			return false
		}
		return true
	})
	return ok
}

// Dispatch is the only API-side callback route. Its closed protobuf oneof has
// no mutation, SQL, space, principal or transaction-mode fields.
func Dispatch(r *postgres.Session, q *p.GraphRequest) (out *p.GraphResponse) {
	out = &p.GraphResponse{CallId: q.CallId}
	fail := func(e error) *p.GraphResponse {
		var m *model.Error
		if !errors.As(e, &m) {
			m = model.E("INTERNAL_ERROR", "Graph operation failed")
		}
		out.Result = &p.GraphResponse_Error{Error: &p.SafeError{Code: m.Code, Message: m.Message, Retryable: m.Retryable}}
		return out
	}
	if !noUnknown(q.ProtoReflect(), 0) {
		return fail(model.E("INCOMPATIBLE_PROTOCOL", "Unknown protocol field"))
	}
	if e := r.Charge(); e != nil {
		return fail(e)
	}
	switch x := q.Operation.(type) {
	case *p.GraphRequest_Lookup:
		if len(x.Lookup.Entities) != 1 {
			return fail(model.Invalid("lookup count"))
		}
		v, e := r.Get(x.Lookup.Entities, x.Lookup.Attributes)
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Entities{Entities: entities(v, true, nil)}
	case *p.GraphRequest_BatchLookup:
		v, e := r.Get(x.BatchLookup.Entities, x.BatchLookup.Attributes)
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Entities{Entities: entities(v, true, nil)}
	case *p.GraphRequest_AttributeRead:
		v, e := r.Values(x.AttributeRead.Entity, x.AttributeRead.Attribute, options(x.AttributeRead.Page))
		if e != nil {
			return fail(e)
		}
		fs := &p.FactPage{Meta: meta(v.Complete, v.NextCursor)}
		for _, f := range v.Items {
			fs.Items = append(fs.Items, entity(model.Entity{Facts: []model.Fact{f}}).Facts[0])
		}
		out.Result = &p.GraphResponse_Facts{Facts: fs}
	case *p.GraphRequest_Refs:
		v, e := r.References(x.Refs.Entity, x.Refs.Attribute, x.Refs.Direction, options(x.Refs.Page))
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Entities{Entities: entities(v.Items, v.Complete, v.NextCursor)}
	case *p.GraphRequest_Incoming:
		if x.Incoming.Direction != "in" {
			return fail(model.Invalid("incoming direction"))
		}
		v, e := r.References(x.Incoming.Entity, x.Incoming.Attribute, "in", options(x.Incoming.Page))
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Entities{Entities: entities(v.Items, v.Complete, v.NextCursor)}
	case *p.GraphRequest_Search:
		q := x.Search
		v, e := r.Search(model.Search{Query: q.Query, Kind: q.Kind, Types: q.Types, PageOptions: options(q.Page)})
		if e != nil {
			return fail(e)
		}
		hits := &p.HitPage{Meta: meta(v.Complete, v.NextCursor)}
		for _, h := range v.Items {
			hits.Items = append(hits.Items, &p.Hit{Id: h.ID, Ident: h.Ident, Kind: h.Kind, Snippet: h.Snippet})
		}
		out.Result = &p.GraphResponse_Hits{Hits: hits}
	case *p.GraphRequest_Find:
		q := x.Find
		pred, e := modelPredicate(q.Where, 0)
		if e != nil {
			return fail(e)
		}
		v, e := r.Find(model.Find{Where: pred, Select: q.Select, Types: q.Types, Query: q.Query, PageOptions: options(q.Page)})
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Entities{Entities: entities(v.Items, v.Complete, v.NextCursor)}
	case *p.GraphRequest_Pull:
		pat, e := modelPattern(x.Pull.Pattern, 0)
		if e != nil {
			return fail(e)
		}
		v, e := r.Pull(x.Pull.Entity, pat)
		if e != nil {
			return fail(e)
		}
		out.Result = &p.GraphResponse_Pull{Pull: pull(v)}
	case *p.GraphRequest_History:
		v, e := r.History(model.History{Entity: x.History.Entity, PageOptions: options(x.History.Page)})
		if e != nil {
			return fail(e)
		}
		h := &p.HistoryPage{Meta: meta(v.Complete, v.NextCursor)}
		for _, ev := range v.Items {
			h.Items = append(h.Items, &p.HistoryEvent{Revision: ev.Revision, Ordinal: uint32(ev.Ordinal), Kind: ev.Kind, Entity: ev.Entity, AuditJson: ev.Payload, Actor: ev.Actor, Client: ev.Client, Reason: ev.Reason, CommittedAt: ev.CommittedAt})
		}
		out.Result = &p.GraphResponse_History{History: h}
	case *p.GraphRequest_Schema:
		q := x.Schema
		v, e := r.Schema(model.SchemaQuery{Action: q.Action, Entity: q.Entity, Query: q.Query, PageOptions: options(q.Page)})
		if e != nil {
			return fail(e)
		}
		defs := &p.DefinitionPage{Meta: meta(true, nil)}
		switch v := v.(type) {
		case model.Definition:
			defs.Items = []*p.Definition{definition(v)}
		case model.Page[model.Definition]:
			defs.Meta = meta(v.Complete, v.NextCursor)
			for _, d := range v.Items {
				defs.Items = append(defs.Items, definition(d))
			}
		default:
			out.Result = &p.GraphResponse_Help{Help: &p.HelpResult{SdkVersion: model.SDKVersion, Documentation: "Read-only synchronous JavaScript. Integers are bigint; decimal/timestamp are exact strings. Use db.schema and db.help()."}}
			return out
		}
		out.Result = &p.GraphResponse_Definitions{Definitions: defs}
	case *p.GraphRequest_Help:
		out.Result = &p.GraphResponse_Help{Help: &p.HelpResult{SdkVersion: model.SDKVersion, Documentation: "Read-only db.entity/entities/search/find/pull/history/schema. get is one, values is many; ref/refs require references. Archival retains history. Integers are bigint; db.toSafeNumber validates exact conversion."}}
	default:
		return fail(model.E("PERMISSION_DENIED", "Unrecognized or forbidden worker operation"))
	}
	if e := r.CheckBudget(); e != nil {
		return fail(e)
	}
	size := proto.Size(out)
	r.Payload += size
	if size > 1<<20 || r.Payload > 4<<20 {
		return fail(model.Limit("Graph payload budget exhausted"))
	}
	return out
}
