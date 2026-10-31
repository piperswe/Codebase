package worker

import (
	"encoding/json"
	"math/big"
	"strconv"

	"codebase.bid/lib/go/jsruntime"
	"codebase.bid/projects/memory/internal/model"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"github.com/dop251/goja"
)

type GraphCall func(*p.GraphRequest) (*p.GraphResponse, error)

func Bind(v *jsruntime.VM, call GraphCall) error {
	r := v.Runtime
	parseValue, e := r.RunString(`(function(p){return function(s){return p(s)}})(JSON.parse)`)
	if e != nil {
		return e
	}
	parse, _ := goja.AssertFunction(parseValue)
	stringifyValue, e := r.RunString(`(function(f){return function(v){return f(v)}})(JSON.stringify)`)
	if e != nil {
		return e
	}
	stringify, _ := goja.AssertFunction(stringifyValue)
	toJS := func(x any) goja.Value {
		b, e := json.Marshal(x)
		if e != nil {
			v.Throw(e)
		}
		val, e := parse(goja.Undefined(), r.ToValue(string(b)))
		if e != nil {
			v.Throw(e)
		}
		return val
	}
	decode := func(arg goja.Value, out any) {
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			arg = r.NewObject()
		}
		s, e := stringify(goja.Undefined(), arg)
		if e != nil {
			v.Throw(e)
		}
		if len(s.String()) > 65536 {
			v.Throw(model.Limit("Graph argument is too large"))
		}
		if e = model.Decode([]byte(s.String()), out); e != nil {
			v.Throw(e)
		}
	}
	invoke := func(q *p.GraphRequest) any {
		res, e := call(q)
		if e != nil {
			v.Throw(e)
		}
		data, e := responseData(res)
		if e != nil {
			v.Throw(e)
		}
		return data
	}
	defs := map[string]model.Definition{}
	describe := func(name string) model.Definition {
		if d, ok := defs[name]; ok {
			return d
		}
		out := invoke(&p.GraphRequest{Operation: &p.GraphRequest_Schema{Schema: &p.Schema{Action: "describe", Entity: name}}}).(model.Page[model.Definition])
		if len(out.Items) != 1 {
			v.Throw(model.E("ATTRIBUTE_NOT_FOUND", "Unknown attribute"))
		}
		d := out.Items[0]
		defs[name] = d
		defs[d.ID] = d
		return d
	}
	cache := map[string]model.Entity{}
	handles := map[string]*goja.Object{}
	var handle func(model.Entity) *goja.Object
	var lookup func(string) *goja.Object
	scalar := func(x model.Value) goja.Value {
		if x.Type == model.Boolean {
			return r.ToValue(x.Bool)
		}
		if x.Type == model.Integer {
			n, _ := new(big.Int).SetString(x.Text, 10)
			return r.ToValue(n)
		}
		return r.ToValue(x.Text)
	}
	define := func(o *goja.Object, name string, x any) {
		if e := o.DefineDataProperty(name, r.ToValue(x), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE); e != nil {
			v.Throw(e)
		}
	}
	lookup = func(name string) *goja.Object {
		if entity, ok := cache[name]; ok {
			return handle(entity)
		}
		out := invoke(&p.GraphRequest{Operation: &p.GraphRequest_Lookup{Lookup: &p.Get{Entities: []string{name}}}}).(model.Page[model.Entity])
		if len(out.Items) != 1 {
			v.Throw(model.E("ENTITY_NOT_FOUND", "Entity is absent"))
		}
		cache[name] = out.Items[0]
		return handle(out.Items[0])
	}
	pageEntities := func(out model.Page[model.Entity]) goja.Value {
		items := r.NewArray()
		for i, e := range out.Items {
			_ = items.Set(strconv.Itoa(i), handle(e))
		}
		o := r.NewObject()
		_ = o.Set("items", items)
		_ = o.Set("complete", out.Complete)
		if out.NextCursor == nil {
			_ = o.Set("nextCursor", goja.Null())
		} else {
			_ = o.Set("nextCursor", *out.NextCursor)
		}
		return o
	}
	handle = func(entity model.Entity) *goja.Object {
		if h := handles[entity.ID]; h != nil {
			return h
		}
		cache[entity.ID] = entity
		if entity.Ident != nil {
			cache[*entity.Ident] = entity
		}
		o := r.NewObject()
		handles[entity.ID] = o
		define(o, "id", entity.ID)
		define(o, "revision", entity.Revision)
		define(o, "archived", entity.Archived)
		if entity.Ident == nil {
			define(o, "ident", goja.Null())
		} else {
			define(o, "ident", *entity.Ident)
		}
		_ = v.RegisterHandle(o, struct {
			ID    string  `json:"id"`
			Ident *string `json:"ident"`
		}{entity.ID, entity.Ident})
		facts := func(d model.Definition) []model.Fact {
			if d.Kind != "attribute" {
				v.Throw(model.E("ATTRIBUTE_NOT_FOUND", "Entity is not a registered attribute"))
			}
			out := []model.Fact{}
			for _, f := range entity.Facts {
				if f.Attribute == d.ID {
					out = append(out, f)
				}
			}
			return out
		}
		_ = v.Function(o, "get", func(c goja.FunctionCall) goja.Value {
			d := describe(c.Argument(0).String())
			if d.Cardinality != "one" {
				v.Throw(model.E("CARDINALITY_VIOLATION", "get requires cardinality one"))
			}
			fs := facts(d)
			if len(fs) == 0 {
				return goja.Null()
			}
			return scalar(fs[0].Value)
		})
		_ = v.Function(o, "ref", func(c goja.FunctionCall) goja.Value {
			d := describe(c.Argument(0).String())
			if d.Cardinality != "one" || d.ValueType != model.Ref {
				v.Throw(model.E("INVALID_VALUE_TYPE", "ref requires a cardinality-one reference"))
			}
			fs := facts(d)
			if len(fs) == 0 {
				return goja.Null()
			}
			return lookup(fs[0].Value.Text)
		})
		for _, direction := range []string{"out", "in"} {
			direction := direction
			name := "refs"
			if direction == "in" {
				name = "incoming"
			}
			_ = v.Function(o, name, func(c goja.FunctionCall) goja.Value {
				d := describe(c.Argument(0).String())
				if d.ValueType != model.Ref || (direction == "out" && d.Cardinality != "many") {
					v.Throw(model.E("INVALID_VALUE_TYPE", "refs requires a many-reference; incoming requires a reference"))
				}
				var opt model.PageOptions
				decode(c.Argument(1), &opt)
				q := &p.References{Entity: entity.ID, Attribute: d.ID, Direction: direction, Page: page(opt)}
				req := &p.GraphRequest{Operation: &p.GraphRequest_Refs{Refs: q}}
				if direction == "in" {
					req.Operation = &p.GraphRequest_Incoming{Incoming: q}
				}
				out := invoke(req).(model.Page[model.Entity])
				return pageEntities(out)
			})
		}
		_ = v.Function(o, "values", func(c goja.FunctionCall) goja.Value {
			d := describe(c.Argument(0).String())
			if d.Cardinality != "many" || d.ValueType == model.Ref {
				v.Throw(model.E("CARDINALITY_VIOLATION", "values requires a many-valued scalar attribute"))
			}
			var opt model.PageOptions
			decode(c.Argument(1), &opt)
			collection := invoke(&p.GraphRequest{Operation: &p.GraphRequest_AttributeRead{AttributeRead: &p.AttributeRead{Entity: entity.ID, Attribute: d.ID, Page: page(opt)}}}).(model.Page[model.Fact])
			fs := collection.Items
			items := r.NewArray()
			for i, f := range fs {
				_ = items.Set(strconv.Itoa(i), scalar(f.Value))
			}
			out := r.NewObject()
			_ = out.Set("items", items)
			_ = out.Set("complete", collection.Complete)
			_ = out.Set("nextCursor", toJS(collection.NextCursor))
			return out
		})
		_ = v.Function(o, "facts", func(c goja.FunctionCall) goja.Value {
			attrs := []string{}
			if !goja.IsUndefined(c.Argument(0)) {
				decode(c.Argument(0), &attrs)
			}
			fs := entity.Facts
			if len(attrs) > 0 {
				fs = []model.Fact{}
				for _, name := range attrs {
					fs = append(fs, facts(describe(name))...)
				}
			}
			return toJS(model.Page[model.Fact]{Items: fs, Complete: true})
		})
		_ = v.Function(o, "pull", func(c goja.FunctionCall) goja.Value {
			var pat model.Pattern
			decode(c.Argument(0), &pat)
			wire, e := pattern(pat)
			if e != nil {
				v.Throw(e)
			}
			return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_Pull{Pull: &p.Pull{Entity: entity.ID, Pattern: wire}}}))
		})
		return o
	}
	db := r.NewObject()
	_ = v.Function(db, "entity", func(c goja.FunctionCall) goja.Value { return lookup(c.Argument(0).String()) })
	_ = v.Function(db, "entities", func(c goja.FunctionCall) goja.Value {
		var ids []string
		decode(c.Argument(0), &ids)
		out := invoke(&p.GraphRequest{Operation: &p.GraphRequest_BatchLookup{BatchLookup: &p.Get{Entities: ids}}}).(model.Page[model.Entity])
		arr := r.NewArray()
		for i, e := range out.Items {
			_ = arr.Set(strconv.Itoa(i), handle(e))
		}
		return arr
	})
	_ = v.Function(db, "search", func(c goja.FunctionCall) goja.Value {
		var q model.Search
		decode(c.Argument(0), &q)
		if q.Space != "" {
			v.Throw(model.Invalid("space is execution-bound"))
		}
		return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_Search{Search: &p.Search{Query: q.Query, Kind: q.Kind, Types: q.Types, Page: page(q.PageOptions)}}}))
	})
	_ = v.Function(db, "find", func(c goja.FunctionCall) goja.Value {
		var q model.Find
		decode(c.Argument(0), &q)
		if q.Space != "" {
			v.Throw(model.Invalid("space is execution-bound"))
		}
		out := invoke(&p.GraphRequest{Operation: &p.GraphRequest_Find{Find: &p.Find{Where: predicate(q.Where), Types: q.Types, Query: q.Query, Page: page(q.PageOptions)}}}).(model.Page[model.Entity])
		return pageEntities(out)
	})
	_ = v.Function(db, "pull", func(c goja.FunctionCall) goja.Value {
		var pat model.Pattern
		decode(c.Argument(1), &pat)
		wire, e := pattern(pat)
		if e != nil {
			v.Throw(e)
		}
		return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_Pull{Pull: &p.Pull{Entity: c.Argument(0).String(), Pattern: wire}}}))
	})
	_ = v.Function(db, "history", func(c goja.FunctionCall) goja.Value {
		var q model.History
		decode(c.Argument(0), &q)
		if q.Space != "" {
			v.Throw(model.Invalid("space is execution-bound"))
		}
		return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_History{History: &p.History{Entity: q.Entity, Page: page(q.PageOptions)}}}))
	})
	schema := r.NewObject()
	for _, action := range []string{"search", "describe", "list", "types"} {
		action := action
		_ = v.Function(schema, action, func(c goja.FunctionCall) goja.Value {
			if action == "describe" {
				return toJS(describe(c.Argument(0).String()))
			}
			var q struct {
				Query string `json:"query,omitempty"`
				model.PageOptions
			}
			decode(c.Argument(0), &q)
			return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_Schema{Schema: &p.Schema{Action: action, Query: q.Query, Page: page(q.PageOptions)}}}))
		})
	}
	define(db, "schema", schema)
	_ = v.Function(db, "help", func(c goja.FunctionCall) goja.Value {
		return toJS(invoke(&p.GraphRequest{Operation: &p.GraphRequest_Help{Help: &p.Help{Topic: c.Argument(0).String()}}}))
	})
	_ = v.Function(db, "toSafeNumber", func(c goja.FunctionCall) goja.Value {
		arg := c.Argument(0)
		if _, ok := arg.(*goja.Object); ok {
			v.Throw(model.Invalid("bigint"))
		}
		n, ok := arg.Export().(*big.Int)
		if !ok || !n.IsInt64() || n.Int64() < -9007199254740991 || n.Int64() > 9007199254740991 {
			v.Throw(model.E("INVALID_VALUE_TYPE", "Integer is outside the JavaScript safe range"))
		}
		return r.ToValue(n.Int64())
	})
	return r.GlobalObject().DefineDataProperty("db", db, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}
