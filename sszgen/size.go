package sszgen

import (
	"fmt"
	"strconv"
	"strings"
)

// size creates a function that returns the SSZ size of the struct. There are two components:
// 1. Fixed: Size that we can determine at compilation time (i.e. uint, fixed bytes, fixed vector...)
// 2. Dynamic: Size that depends on the input (i.e. lists, dynamic containers...)
// Note that if any of the internal fields of the struct is nil, we will not fail, only not add up
// that field to the size. It is up to other methods like marshal to fail on that scenario.
func (e *env) size(name string, v *Value) string {
	tmpl := `// SizeSSZ returns the ssz encoded size in bytes for the {{.name}} object
	func (:: *{{.name}}) SizeSSZ() (size int) {
		size = {{.fixed}}{{if .dynamic}}

		{{.dynamic}}
		{{end}}
		return
	}`

	str := execTmpl(tmpl, map[string]interface{}{
		"name":    name,
		"fixed":   v.valueSize,
		"dynamic": v.sizeContainer("size", true),
	})
	return appendObjSignature(str, v)
}

func (v *Value) sizeContainer(name string, start bool) string {
	if !start {
		tmpl := `{{if .check}} if ::.{{.fieldName}} == nil {
			::.{{.fieldName}} = new({{.structName}})
		}
		{{end}} {{ .dst }} += ::.{{.fieldName}}.SizeSSZ()`

		check := true
		if v.isListElem() {
			check = false
		}
		if v.noPtr {
			check = false
		}
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName":  v.fieldName,
			"dst":   name,
			"structName":   v.objRef(),
			"check": check,
		})
	}
	out := []string{}
	for indx, v := range v.o {
		if !v.isFixed() {
			out = append(out, fmt.Sprintf("// Field (%d) '%s'\n%s", indx, v.fieldName, v.size(name)))
		}
	}
	return strings.Join(out, "\n\n")
}

// 'fieldName' is the fieldName of target variable we assign the size too. We also use this function
// during marshalling to figure out the size of the offset
func (v *Value) size(name string) string {
	if v.isFixed() {
		if v.sszValueType == TypeContainer {
			return v.sizeContainer(name, false)
		}
		if v.valueSize == 1 {
			return name + "++"
		}
		return name + " += " + strconv.Itoa(int(v.valueSize))
	}

	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return v.sizeContainer(name, false)

	case TypeBitList:
		fallthrough

	case TypeBytes:
		return fmt.Sprintf(name+" += len(::.%s)", v.fieldName)

	case TypeList:
		fallthrough

	case TypeVector:
		if v.e.isFixed() {
			return fmt.Sprintf("%s += len(::.%s) * %d", name, v.fieldName, v.e.valueSize)
		}
		v.e.fieldName = v.fieldName + "[ii]"
		tmpl := `for ii := 0; ii < len(::.{{.fieldName}}); ii++ {
			{{.size}} += 4
			{{.dynamic}}
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName":    v.fieldName,
			"size":    name,
			"dynamic": v.e.size(name),
		})

	default:
		panic(fmt.Errorf("size not implemented for type %s", v.sszValueType.String()))
	}
}
