// Package wrangler provides a Gazelle extension for Wrangler type generation.
package wrangler

import (
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const (
	languageName           = "wrangler"
	configFileName         = "wrangler.toml"
	generatedTypesDirName  = "types"
	generatedTypesFileName = "worker-configuration.d.ts"
	generatedTypesPath     = generatedTypesDirName + "/" + generatedTypesFileName
	sourceTypesFileName    = "worker-configuration.d.ts"
	generatedTypesRuleKind = "wrangler.wrangler"
	generatedTypesRuleName = "generated_types"
	typesFileRuleKind      = "directory_path"
	typesFileRuleName      = "generated_types_file"
	typegenRuleKind        = "write_source_files"
	typegenRuleName        = "typegen"
)

type wranglerLang struct {
	language.BaseLang
}

// NewLanguage constructs the Wrangler Gazelle extension.
func NewLanguage() language.Language {
	return &wranglerLang{}
}

func (*wranglerLang) Name() string {
	return languageName
}

func (*wranglerLang) Kinds() map[string]rule.KindInfo {
	return map[string]rule.KindInfo{
		generatedTypesRuleKind: {
			NonEmptyAttrs: map[string]bool{
				"out_dirs": true,
				"srcs":     true,
			},
			SubstituteAttrs: map[string]bool{
				"srcs": true,
			},
			MergeableAttrs: map[string]bool{
				"args":     true,
				"out_dirs": true,
				"srcs":     true,
			},
		},
		typesFileRuleKind: {
			NonEmptyAttrs: map[string]bool{
				"directory": true,
				"path":      true,
			},
		},
		typegenRuleKind: {
			NonEmptyAttrs: map[string]bool{
				"files": true,
			},
			MergeableAttrs: map[string]bool{
				"files": true,
			},
		},
	}
}

func (*wranglerLang) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name:    "//tools/gazelle/wrangler:defs.bzl",
			Symbols: []string{"directory_path", "wrangler", "write_source_files"},
		},
	}
}

func (*wranglerLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	if !containsWranglerConfig(args.RegularFiles) {
		return language.GenerateResult{
			Empty: []*rule.Rule{
				rule.NewRule(generatedTypesRuleKind, generatedTypesRuleName),
				rule.NewRule(typesFileRuleKind, typesFileRuleName),
				rule.NewRule(typegenRuleKind, typegenRuleName),
			},
		}
	}

	generatedTypes := rule.NewRule(generatedTypesRuleKind, generatedTypesRuleName)
	generatedTypes.SetAttr("srcs", []string{configFileName})
	generatedTypes.SetAttr("out_dirs", []string{generatedTypesDirName})
	generatedTypes.SetAttr("args", []string{
		"types",
		"--config",
		configFileName,
		generatedTypesPath,
	})
	generatedTypes.SetAttr("chdir", packageNameCall())
	generatedTypes.SetAttr("env", map[string]string{
		"WRANGLER_WRITE_LOGS": "false",
	})
	typesFile := rule.NewRule(typesFileRuleKind, typesFileRuleName)
	typesFile.SetAttr("directory", ":"+generatedTypesRuleName)
	typesFile.SetAttr("path", generatedTypesFileName)

	typegen := rule.NewRule(typegenRuleKind, typegenRuleName)
	typegen.SetAttr("files", map[string]string{
		sourceTypesFileName: ":" + typesFileRuleName,
	})

	return language.GenerateResult{
		Gen:     []*rule.Rule{generatedTypes, typesFile, typegen},
		Imports: []interface{}{nil, nil, nil},
	}
}

func packageNameCall() interface{} {
	f, err := rule.LoadData("", "", []byte("x(chdir = package_name())"))
	if err != nil {
		panic(err)
	}
	return f.Rules[0].Attr("chdir")
}

func containsWranglerConfig(files []string) bool {
	for _, file := range files {
		if file == configFileName {
			return true
		}
	}
	return false
}
