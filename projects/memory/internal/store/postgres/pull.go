package postgres

import (
	"codebase.bid/projects/memory/internal/model"
)

type projection struct {
	fields   map[string]*projection
	incoming map[string]*projection
	limit    int
}

func (r *Session) projection(p model.Pattern, depth int, nodes *int) (*projection, error) {
	*nodes++
	if depth > 5 || *nodes > 100 || len(p.Select) > 100 || len(p.Incoming) > 20 {
		return nil, model.Limit("Pull pattern exceeds depth/field budget")
	}
	limit, e := pageSize(p.Limit)
	if e != nil {
		return nil, e
	}
	out := &projection{fields: map[string]*projection{}, incoming: map[string]*projection{}, limit: limit}
	for name, raw := range p.Select {
		d, e := r.Definition(name)
		if e != nil {
			return nil, e
		}
		if string(raw) == "true" {
			out.fields[d.ID] = nil
			continue
		}
		if d.ValueType != model.Ref {
			return nil, model.E("INVALID_VALUE_TYPE", "Nested projection requires references")
		}
		var child model.Pattern
		if e = model.Decode(raw, &child); e != nil {
			return nil, e
		}
		sub, e := r.projection(child, depth+1, nodes)
		if e != nil {
			return nil, e
		}
		out.fields[d.ID] = sub
	}
	for _, in := range p.Incoming {
		d, e := r.Definition(in.Attribute)
		if e != nil {
			return nil, e
		}
		if d.ValueType != model.Ref {
			return nil, model.E("INVALID_VALUE_TYPE", "Incoming requires references")
		}
		sub, e := r.projection(model.Pattern{Select: in.Select, Limit: in.Limit}, depth+1, nodes)
		if e != nil {
			return nil, e
		}
		out.incoming[d.ID] = sub
	}
	return out, nil
}
func (r *Session) Pull(entity string, pattern model.Pattern) (model.PullResult, error) {
	var out model.PullResult
	nodes := 0
	p, e := r.projection(pattern, 0, &nodes)
	if e != nil {
		return out, e
	}
	id, e := r.Resolve(entity)
	if e != nil {
		return out, e
	}
	type task struct {
		id        string
		p         *projection
		node      *model.PullResult
		ancestors map[string]bool
	}
	queue := []task{{id, p, &out, map[string]bool{}}}
	for len(queue) > 0 {
		ids := []string{}
		attrs := []string{}
		targets := []string{}
		for _, t := range queue {
			ids = append(ids, t.id)
			for a := range t.p.incoming {
				attrs = append(attrs, a)
				targets = append(targets, t.id)
			}
		}
		ids = stableIDs(ids)
		byID := map[string]model.Entity{}
		for start := 0; start < len(ids); start += 100 {
			end := min(start+100, len(ids))
			batch, e := r.Get(ids[start:end], nil)
			if e != nil {
				return out, e
			}
			for _, v := range batch {
				byID[v.ID] = v
			}
		}
		incoming := map[string][]string{}
		if len(attrs) > 0 {
			if e = r.budget(); e != nil {
				return out, e
			}
			rows, e := r.tx.Query(r.ctx, `SELECT value_ref::text,attribute_id::text,entity_id::text FROM memory.read_facts WHERE space_id=$1 AND attribute_id=ANY($2::uuid[]) AND value_ref=ANY($3::uuid[]) ORDER BY value_ref,attribute_id,entity_id LIMIT 10001`, r.Space, stableIDs(attrs), stableIDs(targets))
			if e != nil {
				return out, safe(e)
			}
			count := 0
			for rows.Next() {
				var target, a, source string
				if e = rows.Scan(&target, &a, &source); e != nil {
					rows.Close()
					return out, safe(e)
				}
				incoming[target+":"+a] = append(incoming[target+":"+a], source)
				count++
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return out, safe(e)
			}
			if count > 10000 {
				return out, model.Limit("Incoming edge scan exceeds row budget")
			}
		}
		next := []task{}
		for _, t := range queue {
			v := byID[t.id]
			facts := v.Facts
			v.Facts = []model.Fact{}
			t.node.Entity = v
			t.node.Edges = map[string]model.Edge{}
			if t.ancestors[t.id] || v.Archived {
				t.node.Stub = true
				continue
			}
			ancestors := map[string]bool{}
			for k, v := range t.ancestors {
				ancestors[k] = v
			}
			ancestors[t.id] = true
			edge := func(a, dir string, child *projection, ids []string) {
				ids = stableIDs(ids)
				ed := model.Edge{Items: []model.PullResult{}, Complete: true}
				if len(ids) > child.limit {
					ids = ids[:child.limit]
					ed.Complete = false
					ed.NextCursor = r.next(ids[len(ids)-1], []string{"refs", t.id, a, dir})
				}
				r.Edges += len(ids)
				ed.Items = make([]model.PullResult, len(ids))
				key := a
				if dir == "in" {
					key = "incoming:" + a
				}
				t.node.Edges[key] = ed
				// Slice backing storage remains stable after assigning the edge value.
				for i, id := range ids {
					next = append(next, task{id, child, &ed.Items[i], ancestors})
				}
			}
			for a, child := range t.p.fields {
				refIDs := []string{}
				for _, f := range facts {
					if f.Attribute == a {
						t.node.Entity.Facts = append(t.node.Entity.Facts, f)
						if f.Value.Type == model.Ref {
							refIDs = append(refIDs, f.Value.Text)
						}
					}
				}
				if child != nil {
					edge(a, "out", child, refIDs)
				}
			}
			for a, child := range t.p.incoming {
				edge(a, "in", child, incoming[t.id+":"+a])
			}
		}
		if e = r.budget(); e != nil {
			return out, e
		}
		queue = next
	}
	return out, nil
}
