package vitest

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestGenerateRules(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "test", file: "vector.test.ts"},
		{name: "spec", file: "vector.spec.ts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewLanguage().GenerateRules(language.GenerateArgs{
				Rel:          "lib/ts/vector",
				RegularFiles: []string{"vector.ts", tt.file},
				OtherGen: []*rule.Rule{
					rule.NewRule("ts_project", "vector_js"),
					rule.NewRule("ts_project", "vector_js_tests"),
				},
			})

			if len(got.Gen) != 1 {
				t.Fatalf("GenerateRules() generated %d rules, want 1", len(got.Gen))
			}
			r := got.Gen[0]
			if r.Kind() != ruleKind || r.Name() != testRuleName {
				t.Fatalf("GenerateRules() generated %s(%q), want %s(%q)", r.Kind(), r.Name(), ruleKind, testRuleName)
			}
			if got := r.AttrStrings("args"); !reflect.DeepEqual(got, []string{"run"}) {
				t.Errorf("args = %v, want [run]", got)
			}
			if got := r.AttrStrings("data"); !reflect.DeepEqual(got, []string{":vector_js", ":vector_js_tests", "//:node_modules/vitest"}) {
				t.Errorf("data = %v, want vector JavaScript targets and Vitest", got)
			}
			if got, want := r.Attr("chdir"), packageNameCall(); !reflect.DeepEqual(got, want) {
				t.Errorf("chdir = %#v, want package_name()", got)
			}
		})
	}
}

func TestGenerateRulesWithoutTests(t *testing.T) {
	got := NewLanguage().GenerateRules(language.GenerateArgs{
		Rel:          "lib/ts/vector",
		RegularFiles: []string{"vector.ts", "vector.test.tsx", "vector.spec.mts"},
	})

	if len(got.Gen) != 0 {
		t.Fatalf("GenerateRules() generated %d rules, want 0", len(got.Gen))
	}
	if len(got.Empty) != 1 || got.Empty[0].Kind() != ruleKind || got.Empty[0].Name() != testRuleName {
		t.Fatalf("GenerateRules() empty rules = %#v, want one empty %s(%q)", got.Empty, ruleKind, testRuleName)
	}
}

func TestGenerateRulesWithoutLibraryTarget(t *testing.T) {
	got := NewLanguage().GenerateRules(language.GenerateArgs{
		Rel:          "lib/ts/standalone",
		RegularFiles: []string{"standalone.test.ts"},
		OtherGen: []*rule.Rule{
			rule.NewRule("ts_project", "standalone_js_tests"),
		},
	})

	want := []string{":standalone_js_tests", "//:node_modules/vitest"}
	if got := got.Gen[0].AttrStrings("data"); !reflect.DeepEqual(got, want) {
		t.Errorf("data = %v, want %v", got, want)
	}
}
