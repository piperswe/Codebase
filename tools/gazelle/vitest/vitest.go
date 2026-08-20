// Package vitest provides a Gazelle extension for Vitest test targets.
package vitest

import (
	"path"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const (
	languageName   = "vitest"
	ruleKind       = "vitest.vitest_test"
	testRuleName   = "test"
	rootTargetName = "root"
)

type vitestLang struct {
	language.BaseLang
}

// NewLanguage constructs the Vitest Gazelle extension.
func NewLanguage() language.Language {
	return &vitestLang{}
}

func (*vitestLang) Name() string {
	return languageName
}

func (*vitestLang) Kinds() map[string]rule.KindInfo {
	return map[string]rule.KindInfo{
		ruleKind: {
			NonEmptyAttrs: map[string]bool{
				"data": true,
			},
			SubstituteAttrs: map[string]bool{
				"data": true,
			},
			MergeableAttrs: map[string]bool{
				"args": true,
				"data": true,
			},
		},
	}
}

func (*vitestLang) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name:    "//tools/gazelle/vitest:defs.bzl",
			Symbols: []string{"vitest"},
		},
	}
}

func (*vitestLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	if !containsTestFile(args.RegularFiles) {
		return language.GenerateResult{
			Empty: []*rule.Rule{rule.NewRule(ruleKind, testRuleName)},
		}
	}

	targetBase := path.Base(args.Rel)
	if args.Rel == "" {
		targetBase = rootTargetName
	}

	libraryTarget := targetBase + "_js"
	testsTarget := targetBase + "_js_tests"
	data := make([]string, 0, 3)
	if containsRule(args.OtherGen, libraryTarget) {
		data = append(data, ":"+libraryTarget)
	}
	data = append(data, ":"+testsTarget, "//:node_modules/vitest")

	r := rule.NewRule(ruleKind, testRuleName)
	r.SetAttr("args", []string{"run"})
	r.SetAttr("chdir", packageNameCall())
	r.SetAttr("data", data)

	return language.GenerateResult{
		Gen:     []*rule.Rule{r},
		Imports: []interface{}{nil},
	}
}

func packageNameCall() interface{} {
	f, err := rule.LoadData("", "", []byte("x(chdir = package_name())"))
	if err != nil {
		panic(err)
	}
	return f.Rules[0].Attr("chdir")
}

func containsTestFile(files []string) bool {
	for _, file := range files {
		if strings.HasSuffix(file, ".test.ts") || strings.HasSuffix(file, ".spec.ts") {
			return true
		}
	}
	return false
}

func containsRule(rules []*rule.Rule, name string) bool {
	for _, r := range rules {
		if r.Name() == name {
			return true
		}
	}
	return false
}
