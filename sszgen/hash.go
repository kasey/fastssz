package sszgen

import (
	"fmt"
	"strings"
)

// hashTreeRoot creates a function that SSZ hashes the structs,
func (e *env) hashTreeRoot(name string, v *Value) string {
	tmpl := `// HashTreeRoot ssz hashes the {{.name}} object
	func (:: *{{.name}}) HashTreeRoot() ([32]byte, error) {
		return ssz.HashWithDefaultHasher(::)
	}
	
	// HashTreeRootWith ssz hashes the {{.name}} object with a hasher
	func (:: *{{.name}}) HashTreeRootWith(hh *ssz.Hasher) (err error) {
		{{.hashTreeRoot}}
		return
	}`

	data := map[string]interface{}{
		"name":         name,
		"hashTreeRoot": v.hashTreeRootContainer(true),
	}
	str := execTmpl(tmpl, data)
	return appendObjSignature(str, v)
}

func (v *Value) hashRoots(isList bool, elem Type) string {
	subName := "i"
	if v.elementType.sizeIsVariable {
		subName += "[:]"
	}
	inner := ""
	if !v.elementType.sizeIsVariable && elem == TypeBytes {
		inner = `if len(i) != %d {
			err = ssz.ErrBytesLength
			return
		}
		`
		inner = fmt.Sprintf(inner, v.elementType.sizeInBytes)
	}

	var appendFn string
	var elemSize uint64
	if elem == TypeBytes {
		// [][]byte
		appendFn = "Append"
		elemSize = 32
	} else {
		// []uint64
		appendFn = "AppendUint64"
		elemSize = 8
	}

	var merkleize string
	if isList {
		tmpl := `numItems := uint64(len(::.{{.fieldName}}))
		hh.MerkleizeWithMixin(subIndx, numItems, ssz.CalculateLimit({{.listSize}}, numItems, {{.elemSize}}))`

		merkleize = execTmpl(tmpl, map[string]interface{}{
			"fieldName":     v.fieldName,
			"listSize": v.sizeInBytes,
			"elemSize": elemSize,
		})

		// when doing []uint64 we need to round up the Hasher bytes to 32
		if elem == TypeUint {
			merkleize = "hh.FillUpTo32()\n" + merkleize
		}
	} else {
		merkleize = "hh.Merkleize(subIndx)"
	}

	tmpl := `{
		{{.outer}}subIndx := hh.Index()
		for _, i := range ::.{{.fieldName}} {
			{{.inner}}hh.{{.appendFn}}({{.subName}})
		}
		{{.merkleize}}
	}`
	return execTmpl(tmpl, map[string]interface{}{
		"outer":     v.validate(),
		"inner":     inner,
		"fieldName":      v.fieldName,
		"subName":   subName,
		"appendFn":  appendFn,
		"merkleize": merkleize,
	})
}

func (v *Value) hashTreeRoot() string {
	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return v.hashTreeRootContainer(false)

	case TypeBytes:
		// There are only fixed []byte
		name := v.fieldName
		if v.sizeIsVariable {
			name += "[:]"
		}

		tmpl := `{{.validate}}hh.PutBytes(::.{{.fieldName}})`
		return execTmpl(tmpl, map[string]interface{}{
			"validate": v.validate(),
			"fieldName":     name,
		})

	case TypeUint:
		var name string
		if v.ref != "" || v.structName != "" {
			// alias to Uint64
			name = fmt.Sprintf("uint64(::.%s)", v.fieldName)
		} else {
			name = "::." + v.fieldName
		}
		bitLen := v.valueSize * 8
		return fmt.Sprintf("hh.PutUint%d(%s)", bitLen, name)

	case TypeBitList:
		tmpl := `if len(::.{{.fieldName}}) == 0 {
			err = ssz.ErrEmptyBitlist
			return
		}
		hh.PutBitlist(::.{{.fieldName}}, {{.size}})
		`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.m,
		})

	case TypeBool:
		return fmt.Sprintf("hh.PutBool(::.%s)", v.fieldName)

	case TypeVector:
		return v.hashRoots(false, v.elementType.sszValueType)

	case TypeList:
		if v.elementType.isFixed() {
			if v.elementType.sszValueType == TypeUint || v.elementType.sszValueType == TypeBytes {
				// return hashBasicSlice(v)
				return v.hashRoots(true, v.elementType.sszValueType)
			}
		}
		tmpl := `{
			subIndx := hh.Index()
			num := uint64(len(::.{{.fieldName}}))
			if num > {{.num}} {
				err = ssz.ErrIncorrectListSize
				return
			}
			for i := uint64(0); i < num; i++ {
				if err = ::.{{.fieldName}}[i].HashTreeRootWith(hh); err != nil {
					return
				}
			}
			hh.MerkleizeWithMixin(subIndx, num, {{.num}})
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"num":  v.m,
		})

	default:
		panic(fmt.Errorf("hash not implemented for type %s", v.sszValueType.String()))
	}
}

func (v *Value) hashTreeRootContainer(start bool) string {
	if !start {
		return fmt.Sprintf("if err = ::.%s.HashTreeRootWith(hh); err != nil {\n return\n}", v.fieldName)
	}

	out := []string{}
	for indx, i := range v.fields {
		str := fmt.Sprintf("// Field (%d) '%s'\n%s\n", indx, i.fieldName, i.hashTreeRoot())
		out = append(out, str)
	}

	tmpl := `indx := hh.Index()

	{{.fields}}

	hh.Merkleize(indx)`

	return execTmpl(tmpl, map[string]interface{}{
		"fields": strings.Join(out, "\n"),
	})
}
