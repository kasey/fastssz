package sszgen

import (
	"fmt"
)

// hashTreeRoot creates a function that SSZ hashes the structs,
func (v *ValueRenderer) RenderHTR() string {
	tmpl := `// HashTreeRoot ssz hashes the {{.StructName}} object
	func ({{.ReceiverName}} *{{.StructName}}) HashTreeRoot() ([32]byte, error) {
		return ssz.HashWithDefaultHasher({{.ReceiverName}})
	}
	
	// HashTreeRootWith ssz hashes the {{.StructName}} object with a hasher
	func ({{.ReceiverName}} *{{.StructName}}) HashTreeRootWith(hh *ssz.Hasher) (err error) {
		indx := hh.Index()
		{{ range .Fields }}
		{{ .HashTreeRoot }}
		{{ end }}
		hh.Merkleize(indx)
		return
	}`

	return execTmpl(tmpl, v)
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
		tmpl := `numItems := uint64(len({{.ReceiverName}}.{{.fieldName}}))
		hh.MerkleizeWithMixin(subIndx, numItems, ssz.CalculateLimit({{.listSize}}, numItems, {{.elemSize}}))`

		merkleize = execTmpl(tmpl, map[string]interface{}{
			"fieldName":     v.fieldName,
			"listSize": v.sizeInBytes,
			"elemSize": elemSize,
			"ReceiverName": v.ReceiverName(),
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
		for _, i := range {{.ReceiverName}}.{{.fieldName}} {
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
		"ReceiverName": v.ReceiverName(),
	})
}

func (v *Value) HashTreeRoot() string {
	comment := fmt.Sprintf("// Field (%d) '%s'\n", v.fieldOffset, v.fieldName)
	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return comment + fmt.Sprintf("if err = %s.%s.HashTreeRootWith(hh); err != nil {\n return\n}", v.ReceiverName(), v.fieldName)

	case TypeBytes:
		// There are only fixed []byte
		name := v.fieldName
		if v.sizeIsVariable {
			name += "[:]"
		}

		tmpl := `{{.validate}}hh.PutBytes({{.ReceiverName}}.{{.fieldName}})`
		return comment + execTmpl(tmpl, map[string]interface{}{
			"validate": v.validate(),
			"fieldName":     name,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeUint:
		var name string
		if v.referencePackageAlias != "" || v.structName != "" {
			// alias to Uint64
			name = fmt.Sprintf("uint64(%s.%s)", v.ReceiverName(), v.fieldName)
		} else {
			name = v.ReceiverName() + "." + v.fieldName
		}
		bitLen := v.valueSize * 8
		return comment + fmt.Sprintf("hh.PutUint%d(%s)", bitLen, name)

	case TypeBitList:
		tmpl := `if len({{.ReceiverName}}.{{.fieldName}}) == 0 {
			err = ssz.ErrEmptyBitlist
			return
		}
		hh.PutBitlist({{.ReceiverName}}.{{.fieldName}}, {{.size}})
		`
		return comment + execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.maxSize,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeBool:
		return fmt.Sprintf("hh.PutBool(%s.%s)", v.ReceiverName(), v.fieldName)

	case TypeVector:
		return v.hashRoots(false, v.elementType.sszValueType)

	case TypeList:
		if v.elementType.IsFixed() {
			if v.elementType.sszValueType == TypeUint || v.elementType.sszValueType == TypeBytes {
				// return hashBasicSlice(v)
				return v.hashRoots(true, v.elementType.sszValueType)
			}
		}
		tmpl := `{
			subIndx := hh.Index()
			num := uint64(len({{.ReceiverName}}.{{.fieldName}}))
			if num > {{.num}} {
				err = ssz.ErrIncorrectListSize
				return
			}
			for i := uint64(0); i < num; i++ {
				if err = {{.ReceiverName}}.{{.fieldName}}[i].HashTreeRootWith(hh); err != nil {
					return
				}
			}
			hh.MerkleizeWithMixin(subIndx, num, {{.num}})
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"num":  v.maxSize,
			"ReceiverName": v.ReceiverName(),
		})

	default:
		panic(fmt.Errorf("hash not implemented for type %s", v.sszValueType.String()))
	}
}