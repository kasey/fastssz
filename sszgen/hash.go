package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const chunkSize = 32

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

func nextChunkAlignment(size int) int {
	if size % chunkSize == 0 {
		return size
	}
	floor := (size / chunkSize) * chunkSize
	return floor + chunkSize
}

func (v *Value) hashRootsInner(elem Type) string {
	var padDeclare, padCopy string
	subName := "i"
	if v.e.c {
		subName += "[:]"
	}
	innerTmpl, _ := template.New("hashRootInnerTemplate").Parse(`{{- .PadDeclare -}}
		if len(i) != {{.ElemSize}} {
			err = ssz.ErrBytesLength
			return
		}
		{{- .PadCopy }}
		hh.{{.AppendFn}}({{.SubName}})`)
	if !v.e.c && elem == TypeBytes {
		// if the size of the byte slice does not align with the htr chunk size
		// we need to zero pad to the next chunk boundary
		paddedSize := nextChunkAlignment(int(v.e.s))
		if paddedSize != int(v.e.s) {
			subName = "padded"
			padDeclare = fmt.Sprintf("padded = make([]byte, %d)\n", paddedSize)
			padCopy = fmt.Sprintf("\ncopy(padded[0:%d], i[0:%d])", v.e.s, v.e.s)
		}
	}
	var appendFn string
	if elem == TypeBytes {
		// [][]byte
		appendFn = "Append"
	} else {
		// []uint64
		appendFn = "AppendUint64"
	}
	buf := bytes.NewBuffer(nil)
	err := innerTmpl.Execute(buf, struct{
		PadDeclare string
		ElemSize uint64
		PadCopy string
		AppendFn string
		SubName string
	}{
		PadDeclare: padDeclare,
		ElemSize: v.e.s,
		PadCopy: padCopy,
		AppendFn: appendFn,
		SubName: subName,
	})
	if err != nil {
		panic(err)
	}

	return string(buf.Bytes())
}

func (v *Value) hashRoots(isList bool, elem Type) string {
	inner := v.hashRootsInner(elem)

	var elemSize uint64
	if elem == TypeBytes {
		// [][]byte
		elemSize = 32
	} else {
		// []uint64
		elemSize = 8
	}

	var merkleize string
	if isList {
		tmpl := `numItems := uint64(len(::.{{.name}}))
		hh.MerkleizeWithMixin(subIndx, numItems, ssz.CalculateLimit({{.listSize}}, numItems, {{.elemSize}}))`

		merkleize = execTmpl(tmpl, map[string]interface{}{
			"name":     v.name,
			"listSize": v.s,
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
		for _, i := range ::.{{.name}} {
			{{.inner}}
		}
		{{.merkleize}}
	}`
	return execTmpl(tmpl, map[string]interface{}{
		"outer":     v.validate(),
		"inner":     inner,
		"name":      v.name,
		"merkleize": merkleize,
	})
}

func (v *Value) hashTreeRoot() string {
	switch v.t {
	case TypeContainer, TypeReference:
		return v.hashTreeRootContainer(false)

	case TypeBytes:
		// There are only fixed []byte
		name := v.name
		if v.c {
			name += "[:]"
		}

		tmpl := `{{.validate}}hh.PutBytes(::.{{.name}})`
		return execTmpl(tmpl, map[string]interface{}{
			"validate": v.validate(),
			"name":     name,
			"size":     v.s,
		})

	case TypeUint:
		var name string
		if v.ref != "" || v.obj != "" {
			// alias to Uint64
			name = fmt.Sprintf("uint64(::.%s)", v.name)
		} else {
			name = "::." + v.name
		}
		bitLen := v.n * 8
		return fmt.Sprintf("hh.PutUint%d(%s)", bitLen, name)

	case TypeBitList:
		tmpl := `if len(::.{{.name}}) == 0 {
			err = ssz.ErrEmptyBitlist
			return
		}
		hh.PutBitlist(::.{{.name}}, {{.size}})
		`
		return execTmpl(tmpl, map[string]interface{}{
			"name": v.name,
			"size": v.m,
		})

	case TypeBool:
		return fmt.Sprintf("hh.PutBool(::.%s)", v.name)

	case TypeVector:
		return v.hashRoots(false, v.e.t)

	case TypeList:
		if v.e.isFixed() {
			if v.e.t == TypeUint || v.e.t == TypeBytes {
				// return hashBasicSlice(v)
				return v.hashRoots(true, v.e.t)
			}
		}
		tmpl := `{
			subIndx := hh.Index()
			num := uint64(len(::.{{.name}}))
			if num > {{.num}} {
				err = ssz.ErrIncorrectListSize
				return
			}
			for i := uint64(0); i < num; i++ {
				if err = ::.{{.name}}[i].HashTreeRootWith(hh); err != nil {
					return
				}
			}
			hh.MerkleizeWithMixin(subIndx, num, {{.num}})
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"name": v.name,
			"num":  v.m,
		})

	default:
		panic(fmt.Errorf("hash not implemented for type %s", v.t.String()))
	}
}

func (v *Value) hashTreeRootContainer(start bool) string {
	if !start {
		return fmt.Sprintf("if err = ::.%s.HashTreeRootWith(hh); err != nil {\n return\n}", v.name)
	}

	out := []string{}
	for indx, i := range v.o {
		str := fmt.Sprintf("// Field (%d) '%s'\n%s\n", indx, i.name, i.hashTreeRoot())
		out = append(out, str)
	}

	tmpl := `indx := hh.Index()

	{{.fields}}

	hh.Merkleize(indx)`

	return execTmpl(tmpl, map[string]interface{}{
		"fields": strings.Join(out, "\n"),
	})
}
