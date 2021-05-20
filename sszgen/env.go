package sszgen

import (
	"fmt"
	"go/ast"
	"path/filepath"
)

func NewEnv(sourcePackage *ast.Package, referencePackages map[string]*ast.Package, sszTypeNames []string) *env {
	sszTypeNameMap := make(map[string]struct{})
	for _, name := range sszTypeNames {
		sszTypeNameMap[name] = struct{}{}
	}
	return &env{
		objs:         make(map[string]*Value),
		referencePackages: referencePackages,
		sourcePackage: sourcePackage,
		sszTypeNames: sszTypeNameMap,
	}
}

// Creates a list of targets for code generation. Some entries in
// e.raw are references, which means we assume their methodset is
// defined elsewhere and skip them. Otherwise we look to see if
// a list of specific targets was specified on the command line.
// If so we only include structs with names in that list, otherwise
// we assume the intention is for all found structs to get a methodset.
func (e *env) CodegenTargets() map[string]*astStruct {
	targets := make(map[string]*astStruct)
	for name, obj := range e.raw {
		if obj.isRef {
			continue
		}
		if len(e.sszTypeNames) > 0 {
			_, ok := e.sszTypeNames[name]
			if !ok {
				continue
			}
		}
		targets[name] = obj
	}
	return targets
}

type env struct {
	// map of structs with their Go AST format
	raw map[string]*astStruct
	// map of structs with their IR format
	objs map[string]*Value
	// map of files with their structs in order
	order map[string][]string
	// target structures to generate ssz methodsets
	sszTypeNames map[string]struct{}
	// imports in all the parsed packages
	imports astImportList
	// sourcePackages replaces 'files', storing
	// parsed source code containing code generation
	// targets at package granularity
	sourcePackage *ast.Package
	// referencePackages replaces 'include'
	// these are packages that do not contain sszTypes we want to do
	// code generation for, but do contain types that we need to reference
	referencePackages map[string]*ast.Package
}

type astImport struct {
	alias string
	path  string
}

func (a *astImport) getFullName() string {
	if a.alias != "" {
		return fmt.Sprintf("%s \"%s\"", a.alias, a.path)
	}
	return fmt.Sprintf("\"%s\"", a.path)
}

func (a *astImport) match(name string) bool {
	if a.alias != "" {
		return a.alias == name
	}
	return filepath.Base(a.path) == name
}

type astImportList []*astImport

func (imps astImportList) find(importName string) string {
	for _, i := range imps {
		if i.match(importName) {
			return i.getFullName()
		}
	}
	return ""
}