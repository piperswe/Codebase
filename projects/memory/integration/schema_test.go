package integration

import (
	"sync"
	"testing"

	"codebase.bid/projects/memory/internal/model"
)

func TestSchemaReviewConcurrency(t *testing.T) {
	s, _ := database(t)
	ctx := t.Context()
	d := model.Definition{Ident: "test/quantity", Kind: "attribute", Description: "Synthetic exact quantity", ValueType: model.Integer, Cardinality: "one"}
	one, e := s.Propose(ctx, owner(), model.Proposal{Space: "personal", Definition: d})
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.Propose(ctx, owner(), model.Proposal{Space: "personal", Definition: d})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, proposal := range []string{one.ID, two.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := s.Apply(ctx, owner(), model.Apply{Space: "personal", Proposal: id, ExpectedSchemaRevision: one.SchemaRevision, Action: "register", Rationale: "Explicit synthetic review"})
			results <- e
		}(proposal)
	}
	wg.Wait()
	close(results)
	ok, stale := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else {
			code(t, e, "SCHEMA_CONFLICT")
			stale++
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatal(ok, stale)
	}
	r := current(t, s)
	registered, e := r.Definition(d.Ident)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	duplicate, e := s.Propose(ctx, owner(), model.Proposal{Space: "personal", Definition: d})
	if e != nil {
		t.Fatal(e)
	}
	if len(duplicate.Candidates) == 0 {
		t.Fatal("duplicate proposal omitted candidates")
	}
	_, e = s.Apply(ctx, owner(), model.Apply{Space: "personal", Proposal: duplicate.ID, ExpectedSchemaRevision: duplicate.SchemaRevision, Action: "register", Rationale: "Deliberate duplicate must fail"})
	code(t, e, "NAME_CONFLICT")
	changed := registered
	changed.ValueType = model.String
	proposal, e := s.Propose(ctx, owner(), model.Proposal{Space: "personal", Definition: changed})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Apply(ctx, owner(), model.Apply{Space: "personal", Proposal: proposal.ID, ExpectedSchemaRevision: proposal.SchemaRevision, Action: "update", Target: registered.ID, Rationale: "Retyping is forbidden"})
	code(t, e, "SCHEMA_CONFLICT")
	alias := d
	alias.Ident = "test/amount"
	proposal, e = s.Propose(ctx, owner(), model.Proposal{Space: "personal", Definition: alias})
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.Apply(ctx, owner(), model.Apply{Space: "personal", Proposal: proposal.ID, ExpectedSchemaRevision: proposal.SchemaRevision, Action: "alias", Target: registered.ID, Rationale: "Same synthetic concept"})
	if e != nil {
		t.Fatal(e)
	}
	r = current(t, s)
	got, e := r.Definition(alias.Ident)
	if e != nil || got.ID != registered.ID || got.SchemaRevision != out.SchemaRevision {
		t.Fatal(got, e)
	}
	canonical, e := r.Definition(registered.Ident)
	if e != nil || canonical.ID != got.ID {
		t.Fatal(canonical, e)
	}
	if out.SchemaRevision == one.SchemaRevision {
		t.Fatal("alias did not advance registry revision")
	}
}
