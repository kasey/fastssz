package sszgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

const bytesPerLengthOffset = 4

// The SSZ code generation works in three steps:
// 1. Parse the Go input with the go/parser library to generate an AST representation.
// 2. Convert the AST into an Internal Representation (IR) to describe the structs and fields
// using the Value object.
// 3. Use the IR to print the encoding functions

func Generate(sourcePath string, dependencyPaths []string, sszTypeNames []string, outputFilename string) error {
	sourcePackage, err := parsePackage(sourcePath) // 1.
	if err != nil {
		return err
	}

	referencePackages:= make(map[string]*ast.Package)
	for _, i := range dependencyPaths {
		pkg, err := parsePackage(i)
		if err != nil {
			return err
		}
		referencePackages[pkg.Name] = pkg
	}

	e := NewEnv(sourcePackage, referencePackages, sszTypeNames)

	if err := e.generateIR(); err != nil { // 2.
		return err
	}

	var out map[string]string
	if outputFilename == "" {
		out, err = e.generateEncodings()
	} else {
		// output to a specific path
		out, err = e.generateOutputEncodings(outputFilename)
	}
	if err != nil {
		return err
	}
	// TODO: push this check up a layer into the output generation
	if out == nil {
		// empty output
		return fmt.Errorf("No files to generate")
	}

	for name, str := range out {
		output := []byte(str)

		output, err = format.Source(output)
		if err != nil {
			return err
		}
		if err := ioutil.WriteFile(name, output, 0644); err != nil {
			return err
		}
	}
	return nil
}

func filterSkipTests(f fs.FileInfo) bool {
	if strings.HasSuffix(f.Name(), "_test.go") {
		return false
	}
	return true
}

func exactMatchFilter(filename string) func(fs.FileInfo) bool {
	return func(f fs.FileInfo) bool {
		return f.Name() == filename
	}
}

func parsePackage(filePath string) (*ast.Package, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	var filt func(fs.FileInfo) bool
	if fileInfo.IsDir() {
		filt = filterSkipTests
	} else {
		filePath = path.Dir(filePath)
		filt = exactMatchFilter(filePath)
	}
	pkgs, err := parser.ParseDir(token.NewFileSet(), filePath, filt, parser.AllErrors)

	// there are only 2 special cases where go allows multiple packages to exist in the same directory
	// 1) A test package can be named <OtherPackageName>_test
	// 2) As long as build tags ignore the package or prevent multiple packages from building in
	// the same pass, the conflicting packages will get filtered out before the compiler cares.
	// for some reason grpc/proto generates placeholder files with `package ignore`
	// and the ignore build tag. The following loop filters out both of these cases so we can pick a single
	// package for each directory up front.
	for k, p := range pkgs {
		if _, ok := pkgs[k + "_test"]; ok {
			delete(pkgs, k)
		}
		for fname, f := range p.Files {
			if len(f.Comments) == 0 && len(f.Decls) == 0 && f.Doc == nil && len(f.Imports) == 0 && f.Scope.Outer == nil && len(f.Scope.Objects) == 0 && len(f.Unresolved) == 0 {
				delete(p.Files, fname)
			}
		}
		if len(p.Files) == 0 {
			delete(pkgs, k)
		}
	}
	if len(pkgs) == 1 {
		for _, v := range pkgs {
			// return the first (and only) thing in the map
			return v, err
		}
	}
	return nil, fmt.Errorf("sszgen only understands source directories with exactly one package (not counting _test and ignored), %s contains %d", filePath, len(pkgs))
}


// Type is a SSZ type
type Type int

const (
	// TypeUint is a SSZ int type
	TypeUint Type = iota
	// TypeBool is a SSZ bool type
	TypeBool
	// TypeBytes is a SSZ fixed or dynamic bytes type
	TypeBytes
	// TypeBitVector is a SSZ bitvector
	TypeBitVector
	// TypeBitList is a SSZ bitlist
	TypeBitList
	// TypeVector is a SSZ vector
	TypeVector
	// TypeList is a SSZ list
	TypeList
	// TypeContainer is a SSZ container
	TypeContainer
	// TypeReference is a SSZ reference
	TypeReference
)

func (t Type) String() string {
	switch t {
	case TypeUint:
		return "uint"
	case TypeBool:
		return "bool"
	case TypeBytes:
		return "bytes"
	case TypeBitVector:
		return "bitvector"
	case TypeBitList:
		return "bitlist"
	case TypeVector:
		return "vector"
	case TypeList:
		return "list"
	case TypeContainer:
		return "container"
	case TypeReference:
		return "reference"
	default:
		panic("not found")
	}
}

const encodingPrefix = "_encoding.go"

func (e *env) generateOutputEncodings(output string) (map[string]string, error) {
	out := map[string]string{}

	keys := make([]string, 0, len(e.order))
	for k := range e.order {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	orders := []string{}
	for _, k := range keys {
		orders = append(orders, e.order[k]...)
	}

	res, ok, err := e.print(true, orders)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	out[output] = res
	return out, nil
}

func (e *env) generateEncodings() (map[string]string, error) {
	outs := map[string]string{}

	firstDone := true
	for name, order := range e.order {
		// remove .go prefix and replace if with our own
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name, ext)
		name += encodingPrefix

		vvv, ok, err := e.print(firstDone, order)
		if err != nil {
			return nil, err
		}
		if ok {
			firstDone = false
			outs[name] = vvv
		}
	}
	return outs, nil
}

func (e *env) hashSource() (string, error) {
	content := ""
	for _, f := range e.sourcePackage.Files {
		var buf bytes.Buffer
		if err := format.Node(&buf, token.NewFileSet(), f); err != nil {
			return "", err
		}
		content += buf.String()
	}

	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:]), nil
}

func (e *env) print(first bool, order []string) (string, bool, error) {
	hash, err := e.hashSource()
	if err != nil {
		return "", false, fmt.Errorf("failed to hash files: %v", err)
	}

	tmpl := `// Code generated by fastssz. DO NOT EDIT.
	// Hash: {{.hash}}
	package {{.package}}

	import (
		ssz "github.com/ferranbt/fastssz" {{ if .imports }}{{ range $value := .imports }}
			{{ $value }} {{ end }}
		{{ end }}
	)

	{{ range .objs }}
		{{ .Marshal }}
		{{ .Unmarshal }}
		{{ .Size }}
		{{ .HashTreeRoot }}
		{{ .GetTree }}
	{{ end }}
	`

	data := map[string]interface{}{
		"package": e.sourcePackage.Name,
		"hash":    hash,
	}

	type Obj struct {
		Size, Marshal, Unmarshal, HashTreeRoot, GetTree string
	}

	objs := []*Obj{}
	uniqImports := make(map[string]struct{})

	// Print the objects in the order in which they appear on the file.
	for _, name := range order {
		obj, ok := e.objs[name]
		if !ok {
			continue
		}

		// detect the imports required to unmarshal this objects
		for _, ref := range obj.detectImports() {
			uniqImports[ref] = struct{}{}
		}

		if obj.isFixed() && obj.isBasicType() {
			// we have an alias of a basic type (uint, bool). These objects
			// will be encoded/decoded inside their parent container and do not
			// require the sszgen functions.
			continue
		}
		getTree := ""
		objs = append(objs, &Obj{
			HashTreeRoot: e.hashTreeRoot(name, obj),
			GetTree:      getTree,
			Marshal:      e.marshal(name, obj),
			Unmarshal:    e.unmarshal(name, obj),
			Size:         e.size(name, obj),
		})
	}
	if len(objs) == 0 {
		// No valid objects found for this file
		return "", false, nil
	}
	data["objs"] = objs

	// insert any required imports

	importsStr := []string{}
	for importName := range uniqImports {
		imp := e.imports.find(importName)
		if imp != "" {
			importsStr = append(importsStr, imp)
		}
	}
	if len(importsStr) != 0 {
		data["imports"] = importsStr
	}

	return execTmpl(tmpl, data), true, nil
}

// All the generated functions use the '::' string to represent the pointer receiver
// of the struct method (i.e 'm' in func(m *Method) XX()) for convenience.
// This function replaces the '::' string with a valid one that corresponds
// to the first letter of the method in lower case.
func appendObjSignature(str string, v *Value) string {
	sig := strings.ToLower(string(v.fieldName[0]))
	return strings.Replace(str, "::", sig, -1)
}

type astStruct struct {
	name     string
	obj      *ast.StructType
	typ      ast.Expr
	implFunc bool
	isRef    bool
}

type astResult struct {
	objs  []*astStruct
	funcs []string
}

func decodeASTStruct(file *ast.File) *astResult {
	res := &astResult{
		objs:  []*astStruct{},
		funcs: []string{},
	}

	funcRefs := map[string]int{}
	for _, dec := range file.Decls {
		if genDecl, ok := dec.(*ast.GenDecl); ok {
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					obj := &astStruct{
						name: typeSpec.Name.Name,
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if ok {
						obj.obj = structType
					} else {
						obj.typ = typeSpec.Type
					}
					res.objs = append(res.objs, obj)
				}
			}
		}
		if funcDecl, ok := dec.(*ast.FuncDecl); ok {
			if funcDecl.Recv == nil {
				continue
			}
			if expr, ok := funcDecl.Recv.List[0].Type.(*ast.StarExpr); ok {
				// only allow pointer functions
				if i, ok := expr.X.(*ast.Ident); ok {
					objName := i.Name
					if ok := isFuncDecl(funcDecl); ok {
						funcRefs[objName]++
					}
				}
			}
		}
	}
	for name, count := range funcRefs {
		if count == 4 {
			// it implements all the interface functions
			res.funcs = append(res.funcs, name)
		}
	}
	return res
}

// when an ast.FuncDecl name matches one of the generated functions known
// by isFuncDecl, it uses isSpecificFunc to compare the signature of the found
// function with a string representation of the expected function signature
// to determine if it found a function generated in a previous run of ssz-gen
func isSpecificFunc(funcDecl *ast.FuncDecl, in, out []string) bool {
	check := func(types *ast.FieldList, args []string) bool {
		list := types.List
		if len(list) != len(args) {
			return false
		}

		for i := 0; i < len(list); i++ {
			typ := list[i].Type
			arg := args[i]

			var buf bytes.Buffer
			fset := token.NewFileSet()
			if err := format.Node(&buf, fset, typ); err != nil {
				panic(err)
			}
			if string(buf.Bytes()) != arg {
				return false
			}
		}

		return true
	}
	if !check(funcDecl.Type.Params, in) {
		return false
	}
	if !check(funcDecl.Type.Results, out) {
		return false
	}
	return true
}

func isFuncDecl(funcDecl *ast.FuncDecl) bool {
	name := funcDecl.Name.Name
	if name == "SizeSSZ" {
		return isSpecificFunc(funcDecl, []string{}, []string{"int"})
	}
	if name == "MarshalSSZTo" {
		return isSpecificFunc(funcDecl, []string{"[]byte"}, []string{"[]byte", "error"})
	}
	if name == "UnmarshalSSZ" {
		return isSpecificFunc(funcDecl, []string{"[]byte"}, []string{"error"})
	}
	if name == "HashTreeRootWith" {
		return isSpecificFunc(funcDecl, []string{"*ssz.Hasher"}, []string{"error"})
	}
	return false
}

func decodeASTImports(file *ast.File) []*astImport {
	imports := []*astImport{}
	for _, i := range file.Imports {
		var alias string
		if i.Name != nil {
			if i.Name.Name == "_" {
				continue
			}
			alias = i.Name.Name
		}
		path := strings.Trim(i.Path.Value, "\"")
		imports = append(imports, &astImport{
			alias: alias,
			path:  path,
		})
	}
	return imports
}

func (e *env) generateIR() error {
	e.raw = map[string]*astStruct{}
	e.order = map[string][]string{}
	e.imports = []*astImport{}

	// we want to make sure we only include one reference for each struct name
	// among the source and include paths.
	addStructs := func(res *astResult, isRef bool) error {
		for _, i := range res.objs {
			if _, ok := e.raw[i.name]; ok {
				return fmt.Errorf("two structs share the same name %s", i.name)
			}
			i.isRef = isRef
			e.raw[i.name] = i
		}
		return nil
	}

	checkImplFunc := func(res *astResult) error {
		// include all the functions that implement the interfaces
		for _, name := range res.funcs {
			v, ok := e.raw[name]
			if !ok {
				return fmt.Errorf("cannot find %s struct", name)
			}
			v.implFunc = true
		}
		return nil
	}

	// add the imports to the environment, we want to make sure that we always import
	// the package with the same name and alias which is easier to logic with.
	addImports := func(imports []*astImport) error {
		for _, i := range imports {
			// check if we already have this import before
			found := false
			for _, j := range e.imports {
				if j.path == i.path {
					found = true
					if i.alias != j.alias {
						return fmt.Errorf("the same package is imported twice by different files of path %s and %s with different aliases: %s and %s", j.path, i.path, j.alias, i.alias)
					}
				}
			}
			if !found {
				e.imports = append(e.imports, i)
			}
		}
		return nil
	}

	// decode all the imports from the input files
	for _, file := range e.sourcePackage.Files {
		if err := addImports(decodeASTImports(file)); err != nil {
			return err
		}
	}

	astResults := []*astResult{}

	// decode the structs from the input path
	for name, file := range e.sourcePackage.Files {
		res := decodeASTStruct(file)
		if err := addStructs(res, false); err != nil {
			return err
		}

		astResults = append(astResults, res)

		// keep the ordering in which the structs appear so that we always generate them in
		// the same predictable order
		structOrdering := []string{}
		for _, i := range res.objs {
			structOrdering = append(structOrdering, i.name)
		}
		e.order[name] = structOrdering
	}

	// decode the structs from the include path but ONLY include them on 'raw' not in 'order'.
	// If the structs are in raw they can be used as a reference at compilation time and since they are
	// not in 'order' they cannot be used to marshal/unmarshal encodings
	for _, pkg := range e.referencePackages {
		for _, file := range pkg.Files {
			res := decodeASTStruct(file)
			if err := addStructs(res, true); err != nil {
				return err
			}

			astResults = append(astResults, res)
		}
	}

	for _, res := range astResults {
		if err := checkImplFunc(res); err != nil {
			return err
		}
	}

	for name, _ := range e.CodegenTargets() {
		if _, err := e.encodeItem(name, ""); err != nil {
			return err
		}
	}
	return nil
}

func (e *env) encodeItem(name, tags string) (*Value, error) {
	v, ok := e.objs[name]
	if !ok {
		var err error
		raw, ok := e.raw[name]
		if !ok {
			return nil, fmt.Errorf("could not find struct with name '%s'", name)
		}
		if raw.implFunc {
			size, _ := getTagsInt(tags, "ssz-size")
			v = &Value{sszValueType: TypeReference, sizeInBytes: size, valueSize: size, noPtr: raw.obj == nil}
		} else if raw.obj != nil {
			v, err = e.parseASTStructType(name, raw.obj)
		} else {
			v, err = e.parseASTFieldType(name, tags, raw.typ)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to encode %s: %v", name, err)
		}
		v.fieldName = name
		v.structName = name
		e.objs[name] = v
	}
	return v.copy(), nil
}

// parse the Go AST struct
func (e *env) parseASTStructType(name string, typ *ast.StructType) (*Value, error) {
	v := &Value{
		fieldName:    name,
		sszValueType: TypeContainer,
		fields:       []*Value{},
	}

	for _, f := range typ.Fields.List {
		if len(f.Names) != 1 {
			continue
		}
		name := f.Names[0].Name
		if !isExportedField(name) {
			continue
		}
		if strings.HasPrefix(name, "XXX_") {
			// skip protobuf methods
			continue
		}
		var tags string
		if f.Tag != nil {
			tags = f.Tag.Value
		}

		elem, err := e.parseASTFieldType(name, tags, f.Type)
		if err != nil {
			return nil, err
		}
		if elem == nil {
			continue
		}
		elem.fieldName = name
		v.fields = append(v.fields, elem)
	}

	// get the total size of the container
	for _, f := range v.fields {
		if f.isFixed() {
			v.valueSize += f.valueSize
		} else {
			v.valueSize += bytesPerLengthOffset
			// container is dynamic
			v.sizeIsVariable = true
		}
	}
	return v, nil
}

func getObjLen(obj *ast.ArrayType) uint64 {
	if obj.Len == nil {
		return 0
	}
	value := obj.Len.(*ast.BasicLit).Value
	num, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		panic(fmt.Sprintf("BUG: Failed to convert to uint64 %s: %v", value, err))
	}
	return num
}

// parse the Go AST field
func (e *env) parseASTFieldType(name, tags string, expr ast.Expr) (*Value, error) {
	if tag, ok := getTags(tags, "ssz"); ok && tag == "-" {
		// omit value
		return nil, nil
	}

	switch obj := expr.(type) {
	case *ast.StarExpr:
		// *Struct
		switch elem := obj.X.(type) {
		case *ast.Ident:
			// reference to a local package
			return e.encodeItem(elem.Name, tags)

		case *ast.SelectorExpr:
			// reference of the external package
			ref := elem.X.(*ast.Ident).Name
			// reference to a struct from another package
			v, err := e.encodeItem(elem.Sel.Name, tags)
			if err != nil {
				return nil, err
			}
			v.referencePackageAlias = ref
			return v, nil

		default:
			return nil, fmt.Errorf("cannot handle %s", elem)
		}

	case *ast.ArrayType:
		if isByte(obj.Elt) {
			if fixedlen := getObjLen(obj); fixedlen != 0 {
				// array of fixed size
				return &Value{sszValueType: TypeBytes, sizeIsVariable: true, sizeInBytes: fixedlen, valueSize: fixedlen}, nil
			}
			// []byte
			if tag, ok := getTags(tags, "ssz"); ok && tag == "bitlist" {
				// bitlist requires a ssz-max field
				max, ok := getTagsInt(tags, "ssz-max")
				if !ok {
					return nil, fmt.Errorf("bitfield requires a 'ssz-max' field")
				}
				return &Value{sszValueType: TypeBitList, maxSize: max, sizeInBytes: max}, nil
			}
			size, ok := getTagsInt(tags, "ssz-size")
			if ok {
				// fixed bytes
				return &Value{sszValueType: TypeBytes, sizeInBytes: size, valueSize: size}, nil
			}
			max, ok := getTagsInt(tags, "ssz-max")
			if !ok {
				return nil, fmt.Errorf("[]byte expects either ssz-max or ssz-size")
			}
			// dynamic bytes
			return &Value{sszValueType: TypeBytes, maxSize: max}, nil
		}
		if isArray(obj.Elt) && isByte(obj.Elt.(*ast.ArrayType).Elt) {
			f, fCheck, s, sCheck, t, err := getRootSizes(obj, tags)
			if err != nil {
				return nil, err
			}
			if t == TypeVector {
				// vector
				return &Value{sszValueType: TypeVector, sizeIsVariable: fCheck, valueSize: f * s, sizeInBytes: f, elementType: &Value{sszValueType: TypeBytes, sizeIsVariable: sCheck, valueSize: s, sizeInBytes: s}}, nil
			}
			// list
			return &Value{sszValueType: TypeList, sizeInBytes: f, elementType: &Value{sszValueType: TypeBytes, sizeIsVariable: sCheck, valueSize: s, sizeInBytes: s}}, nil
		}

		// []*Struct
		elem, err := e.parseASTFieldType(name, tags, obj.Elt)
		if err != nil {
			return nil, err
		}
		if size, ok := getTagsInt(tags, "ssz-size"); ok {
			// fixed vector
			v := &Value{sszValueType: TypeVector, sizeInBytes: size, elementType: elem}
			if elem.isFixed() {
				// set the total size
				v.valueSize = size * elem.valueSize
			}
			return v, err
		}
		// list
		maxSize, ok := getTagsInt(tags, "ssz-max")
		if !ok {
			return nil, fmt.Errorf("slice '%s' expects either ssz-max or ssz-size", name)
		}
		v := &Value{sszValueType: TypeList, elementType: elem, sizeInBytes: maxSize, maxSize: maxSize}
		return v, nil

	case *ast.Ident:
		// basic type
		var v *Value
		switch obj.Name {
		case "uint64":
			v = &Value{sszValueType: TypeUint, valueSize: 8}
		case "uint32":
			v = &Value{sszValueType: TypeUint, valueSize: 4}
		case "uint16":
			v = &Value{sszValueType: TypeUint, valueSize: 2}
		case "uint8":
			v = &Value{sszValueType: TypeUint, valueSize: 1}
		case "bool":
			v = &Value{sszValueType: TypeBool, valueSize: 1}
		default:
			// try to resolve as an alias
			vv, err := e.encodeItem(obj.Name, tags)
			if err != nil {
				return nil, fmt.Errorf("type %s not found", obj.Name)
			}
			return vv, nil
		}
		return v, nil

	case *ast.SelectorExpr:
		name := obj.X.(*ast.Ident).Name
		sel := obj.Sel.Name

		if sel == "Bitlist" {
			// go-bitfield/Bitlist
			maxSize, ok := getTagsInt(tags, "ssz-max")
			if !ok {
				return nil, fmt.Errorf("bitlist %s does not have ssz-max tag", name)
			}
			return &Value{sszValueType: TypeBitList, maxSize: maxSize, sizeInBytes: maxSize}, nil
		} else if strings.HasPrefix(sel, "Bitvector") {
			// go-bitfield/Bitvector, fixed bytes
			size, ok := getTagsInt(tags, "ssz-size")
			if !ok {
				return nil, fmt.Errorf("bitvector %s does not have ssz-size tag", name)
			}
			return &Value{sszValueType: TypeBytes, sizeInBytes: size, valueSize: size}, nil
		}
		// external reference
		vv, err := e.encodeItem(sel, tags)
		if err != nil {
			return nil, err
		}
		vv.referencePackageAlias = name
		vv.noPtr = true
		return vv, nil

	default:
		panic(fmt.Errorf("ast type '%s' not expected", reflect.TypeOf(expr)))
	}
}

func getRootSizes(obj *ast.ArrayType, tags string) (f uint64, fCheck bool, s uint64, sCheck bool, t Type, err error) {

	// check if we are in an array and we get the sizes from there
	f = getObjLen(obj)
	s = getObjLen(obj.Elt.(*ast.ArrayType))
	t = TypeVector

	if f != 0 {
		fCheck = true
	}
	if s != 0 {
		sCheck = true
	}

	if f != 0 && s != 0 {
		// all the sizes are set as arrays
		return
	}

	if f != s {
		// one of the values was not set as an array
		// check 'ssz-size' for vector or 'ssz-max' for a list
		size, ok := getTagsInt(tags, "ssz-size")
		if !ok {
			t = TypeList
			size, ok = getTagsInt(tags, "ssz-max")
			if !ok {
				err = fmt.Errorf("bad")
				return
			}
		}

		// fill the missing size
		if f == 0 {
			f = size
		} else {
			s = size
		}
		return
	}

	// Neither of the values was set as an array, we need
	// to get both sizes with the go tags
	var ok bool
	f, s, ok = getTagsTuple(tags, "ssz-size")
	if !ok {
		err = fmt.Errorf("[][]byte expects a ssz-size tag")
		return
	}
	if f == 0 {
		t = TypeList
		f, ok = getTagsInt(tags, "ssz-max")
		if !ok {
			err = fmt.Errorf("ssz-max not set after '?' field on ssz-size")
			return
		}
	}
	return
}

func isArray(obj ast.Expr) bool {
	_, ok := obj.(*ast.ArrayType)
	return ok
}

func isByte(obj ast.Expr) bool {
	if ident, ok := obj.(*ast.Ident); ok {
		if ident.Name == "byte" {
			return true
		}
	}
	return false
}

func isExportedField(str string) bool {
	return str[0] <= 90
}

// getTagsTuple decodes tags of the format 'ssz-size:"33,32"'. If the
// first value is '?' it returns -1.
func getTagsTuple(str string, field string) (uint64, uint64, bool) {
	tupleStr, ok := getTags(str, field)
	if !ok {
		return 0, 0, false
	}

	spl := strings.Split(tupleStr, ",")
	if len(spl) != 2 {
		return 0, 0, false
	}

	// first can be either ? or a number
	var first uint64
	if spl[0] == "?" {
		first = 0
	} else {
		tmp, err := strconv.Atoi(spl[0])
		if err != nil {
			return 0, 0, false
		}
		first = uint64(tmp)
	}

	second, err := strconv.Atoi(spl[1])
	if err != nil {
		return 0, 0, false
	}
	return first, uint64(second), true
}

// getTagsInt returns tags of the format 'ssz-size:"32"'
func getTagsInt(str string, field string) (uint64, bool) {
	numStr, ok := getTags(str, field)
	if !ok {
		return 0, false
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, false
	}
	return uint64(num), true
}

// getTags returns the tags from a given field
func getTags(str string, field string) (string, bool) {
	str = strings.Trim(str, "`")

	for _, tag := range strings.Split(str, " ") {
		if !strings.Contains(tag, ":") {
			return "", false
		}
		spl := strings.Split(tag, ":")
		if len(spl) != 2 {
			return "", false
		}

		tagName, vals := spl[0], spl[1]
		if !strings.HasPrefix(vals, "\"") || !strings.HasSuffix(vals, "\"") {
			return "", false
		}
		if tagName != field {
			continue
		}

		vals = strings.Trim(vals, "\"")
		return vals, true
	}
	return "", false
}

func execTmpl(tpl string, input interface{}) string {
	tmpl, err := template.New("tmpl").Parse(tpl)
	if err != nil {
		panic(err)
	}
	buf := new(bytes.Buffer)
	if err = tmpl.Execute(buf, input); err != nil {
		panic(err)
	}
	return buf.String()
}

