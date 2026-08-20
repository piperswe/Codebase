package wrangler

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestGenerateRules(t *testing.T) {
	got := NewLanguage().GenerateRules(language.GenerateArgs{
		Rel:          "projects/worker",
		RegularFiles: []string{"index.ts", configFileName},
	})

	if len(got.Gen) != 3 {
		t.Fatalf("GenerateRules() generated %d rules, want 3", len(got.Gen))
	}
	if len(got.Imports) != 3 {
		t.Fatalf("GenerateRules() returned %d imports, want 3", len(got.Imports))
	}

	generatedTypes := got.Gen[0]
	if generatedTypes.Kind() != generatedTypesRuleKind || generatedTypes.Name() != generatedTypesRuleName {
		t.Fatalf(
			"GenerateRules() generated %s(%q), want %s(%q)",
			generatedTypes.Kind(),
			generatedTypes.Name(),
			generatedTypesRuleKind,
			generatedTypesRuleName,
		)
	}
	assertStringAttr(t, generatedTypes, "srcs", []string{configFileName})
	assertStringAttr(t, generatedTypes, "out_dirs", []string{generatedTypesDirName})
	assertStringAttr(t, generatedTypes, "args", []string{
		"types",
		"--config",
		configFileName,
		generatedTypesPath,
	})
	if got, want := generatedTypes.Attr("chdir"), packageNameCall(); !reflect.DeepEqual(got, want) {
		t.Errorf("chdir = %#v, want package_name()", got)
	}
	if got, want := generatedTypes.Attr("env"), rule.ExprFromValue(map[string]string{"WRANGLER_WRITE_LOGS": "false"}); !reflect.DeepEqual(got, want) {
		t.Errorf("env = %#v, want %#v", got, want)
	}

	typesFile := got.Gen[1]
	if typesFile.Kind() != typesFileRuleKind || typesFile.Name() != typesFileRuleName {
		t.Fatalf(
			"GenerateRules() generated %s(%q), want %s(%q)",
			typesFile.Kind(),
			typesFile.Name(),
			typesFileRuleKind,
			typesFileRuleName,
		)
	}
	if got, want := typesFile.AttrString("directory"), ":"+generatedTypesRuleName; got != want {
		t.Errorf("directory = %q, want %q", got, want)
	}
	if got := typesFile.AttrString("path"); got != generatedTypesFileName {
		t.Errorf("path = %q, want %q", got, generatedTypesFileName)
	}

	typegen := got.Gen[2]
	if typegen.Kind() != typegenRuleKind || typegen.Name() != typegenRuleName {
		t.Fatalf(
			"GenerateRules() generated %s(%q), want %s(%q)",
			typegen.Kind(),
			typegen.Name(),
			typegenRuleKind,
			typegenRuleName,
		)
	}
	if got, want := typegen.Attr("files"), rule.ExprFromValue(map[string]string{sourceTypesFileName: ":" + typesFileRuleName}); !reflect.DeepEqual(got, want) {
		t.Errorf("files = %#v, want %#v", got, want)
	}
}

func TestGenerateRulesWithoutWranglerConfig(t *testing.T) {
	got := NewLanguage().GenerateRules(language.GenerateArgs{
		Rel:          "projects/worker",
		RegularFiles: []string{"index.ts", "wrangler.jsonc"},
	})

	if len(got.Gen) != 0 {
		t.Fatalf("GenerateRules() generated %d rules, want 0", len(got.Gen))
	}
	if len(got.Empty) != 3 {
		t.Fatalf("GenerateRules() returned %d empty rules, want 3", len(got.Empty))
	}
	if got.Empty[0].Kind() != generatedTypesRuleKind || got.Empty[0].Name() != generatedTypesRuleName {
		t.Errorf("first empty rule = %s(%q), want %s(%q)", got.Empty[0].Kind(), got.Empty[0].Name(), generatedTypesRuleKind, generatedTypesRuleName)
	}
	if got.Empty[1].Kind() != typesFileRuleKind || got.Empty[1].Name() != typesFileRuleName {
		t.Errorf("second empty rule = %s(%q), want %s(%q)", got.Empty[1].Kind(), got.Empty[1].Name(), typesFileRuleKind, typesFileRuleName)
	}
	if got.Empty[2].Kind() != typegenRuleKind || got.Empty[2].Name() != typegenRuleName {
		t.Errorf("third empty rule = %s(%q), want %s(%q)", got.Empty[2].Kind(), got.Empty[2].Name(), typegenRuleKind, typegenRuleName)
	}
}

func assertStringAttr(t *testing.T, r *rule.Rule, attr string, want []string) {
	t.Helper()
	if got := r.AttrStrings(attr); !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", attr, got, want)
	}
}
