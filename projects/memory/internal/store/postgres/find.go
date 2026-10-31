package postgres

import (
	"codebase.bid/projects/memory/internal/model"
	"fmt"
	"strings"
	"time"
)

type compiler struct {
	r     *Session
	args  []any
	nodes int
	alias int
}

func (c *compiler) param(v any) string {
	c.args = append(c.args, v)
	return fmt.Sprintf("$%d", len(c.args))
}
func (c *compiler) predicate(p model.Predicate, entity string, depth int) (string, error) {
	c.nodes++
	if c.nodes > 64 || depth > 8 || len(p.Path) > 4 {
		return "", model.Limit("Predicate depth/term/path limit exceeded")
	}
	if len(p.All) > 0 || len(p.Any) > 0 {
		if (len(p.All) > 0 && len(p.Any) > 0) || p.Op != "" || p.Attribute != "" || len(p.Path) > 0 || p.Value != nil || len(p.Values) > 0 {
			return "", model.Invalid("predicate union")
		}
		terms := p.All
		join := " AND "
		if len(p.Any) > 0 {
			terms = p.Any
			join = " OR "
		}
		parts := []string{}
		for _, term := range terms {
			x, e := c.predicate(term, entity, depth+1)
			if e != nil {
				return "", e
			}
			parts = append(parts, x)
		}
		return "(" + strings.Join(parts, join) + ")", nil
	}
	if len(p.Path) > 0 {
		step := p.Path[0]
		d, e := c.r.Definition(step.Attribute)
		if e != nil {
			return "", e
		}
		if d.ValueType != model.Ref {
			return "", model.E("INVALID_VALUE_TYPE", "Path requires reference attributes")
		}
		if step.Direction != "in" && step.Direction != "out" {
			return "", model.Invalid("path direction")
		}
		c.alias++
		a := fmt.Sprintf("p%d", c.alias)
		col, link := "value_ref", "entity_id"
		if step.Direction == "in" {
			col, link = "entity_id", "value_ref"
		}
		p.Path = p.Path[1:]
		inner, e := c.predicate(p, a+"."+col, depth+1)
		if e != nil {
			return "", e
		}
		return "EXISTS(SELECT 1 FROM memory.read_facts " + a + " WHERE " + a + ".space_id=$1 AND " + a + ".attribute_id=" + c.param(d.ID) + "::uuid AND " + a + "." + link + "=" + entity + " AND " + inner + ")", nil
	}
	d, e := c.r.Definition(p.Attribute)
	if e != nil {
		return "", e
	}
	c.alias++
	a := fmt.Sprintf("f%d", c.alias)
	base := "EXISTS(SELECT 1 FROM memory.read_facts " + a + " WHERE " + a + ".space_id=$1 AND " + a + ".entity_id=" + entity + " AND " + a + ".attribute_id=" + c.param(d.ID) + "::uuid"
	if p.Op == "exists" {
		if p.Value != nil || len(p.Values) > 0 {
			return "", model.Invalid("exists predicate")
		}
		return base + ")", nil
	}
	ops := map[string]string{"eq": "=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}
	op, ok := ops[p.Op]
	if !ok && p.Op != "in" {
		return "", model.Invalid("predicate operator")
	}
	if p.Op != "eq" && p.Op != "in" && (d.ValueType != model.Integer && d.ValueType != model.Decimal && d.ValueType != model.Timestamp) {
		return "", model.E("INVALID_VALUE_TYPE", "Ordering is limited to numeric and timestamp attributes")
	}
	values := p.Values
	if p.Op != "in" {
		if p.Value == nil || len(p.Values) > 0 {
			return "", model.Invalid("predicate value")
		}
		values = []model.Value{*p.Value}
	} else {
		if p.Value != nil || len(values) == 0 || len(values) > 50 {
			return "", model.Limit("in requires 1..50 values")
		}
		op = "="
	}
	parts := []string{}
	for _, v := range values {
		if v.Type != d.ValueType {
			return "", model.E("INVALID_VALUE_TYPE", "Predicate type does not match schema")
		}
		if v.Type == model.Ref {
			id, e := c.r.Resolve(v.Text)
			if e != nil {
				return "", e
			}
			v.Text = id
		}
		v, e = v.Canonical()
		if e != nil {
			return "", e
		}
		col, cast := "value_text", "text"
		var arg any = v.Text
		switch v.Type {
		case model.Boolean:
			col, cast, arg = "value_bool", "boolean", v.Bool
		case model.Integer:
			col, cast = "value_integer", "bigint"
		case model.Decimal:
			col, cast = "value_decimal", "numeric"
		case model.Timestamp:
			col, cast = "value_timestamp", "timestamptz"
			arg, _ = time.Parse(time.RFC3339Nano, v.Text)
		case model.Ref:
			col, cast = "value_ref", "uuid"
		}
		parts = append(parts, a+"."+col+op+c.param(arg)+"::"+cast)
	}
	return base + " AND (" + strings.Join(parts, " OR ") + ") )", nil
}
func (r *Session) Find(req model.Find) (model.Page[model.Entity], error) {
	out := model.Page[model.Entity]{Items: []model.Entity{}, Complete: true}
	q := req
	q.Cursor = ""
	q.Limit = 0
	last, n, e := r.after(req.PageOptions, q)
	if e != nil {
		return out, e
	}
	if last == "" {
		last = "00000000-0000-0000-0000-000000000000"
	}
	if len(req.Query) > 500 || len(req.Types) > 20 {
		return out, model.Limit("Find filters too large")
	}
	c := &compiler{r: r, args: []any{r.Space}}
	where, e := c.predicate(req.Where, "e.id", 0)
	if e != nil {
		return out, e
	}
	for _, typ := range req.Types {
		id, e := r.TypeID(typ)
		if e != nil {
			return out, e
		}
		v := model.Value{Type: model.Ref, Text: id}
		p, e := c.predicate(model.Predicate{Attribute: "entity/type", Op: "eq", Value: &v}, "e.id", 0)
		if e != nil {
			return out, e
		}
		where += " AND " + p
	}
	if req.Query != "" {
		where += " AND EXISTS(SELECT 1 FROM memory.entity_search s WHERE s.space_id=e.space_id AND s.entity_id=e.id AND s.tsv @@ plainto_tsquery('simple'," + c.param(req.Query) + "))"
	}
	sql := `SELECT e.id::text FROM memory.entities e WHERE e.space_id=$1 AND e.archived_revision IS NULL AND e.id>` + c.param(last) + `::uuid AND ` + where + ` ORDER BY e.id LIMIT ` + c.param(n+1)
	if e = r.budget(); e != nil {
		return out, e
	}
	rows, e := r.tx.Query(r.ctx, sql, c.args...)
	if e != nil {
		return out, safe(e)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, safe(e)
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, safe(e)
	}
	if len(ids) > n {
		ids = ids[:n]
		out.Complete = false
		out.NextCursor = r.next(ids[n-1], q)
	}
	if len(ids) > 0 {
		out.Items, e = r.Get(ids, req.Select)
	}
	return out, e
}
