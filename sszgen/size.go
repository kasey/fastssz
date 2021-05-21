package sszgen

import (
	"fmt"
	"strconv"
)

// size creates a function that returns the SSZ size of the struct. There are two components:
// 1. Fixed: Size that we can determine at compilation time (i.e. uint, fixed bytes, fixed vector...)
// 2. Dynamic: Size that depends on the input (i.e. lists, dynamic containers...)
// Note that if any of the internal fields of the struct is nil, we will not fail, only not add up
// that field to the size. It is up to other methods like marshal to fail on that scenario.
func (v *ValueRenderer) SizeSSZ() string {
	tmpl := `// SizeSSZ returns the ssz encoded size in bytes for the {{.StructName}} object
	func ({{.ReceiverName}} *{{.StructName}}) SizeSSZ() (size int) {
		size = {{.ValueSize}}
		{{ range .Fields -}}
			{{ if not .IsFixed }}
		// Field ({{.FieldOffset}}) '{{.FieldName}}'
		{{.Size}}
			{{- end -}}
		{{- end -}}
		return
	}`

	return execTmpl(tmpl, v)
}

func (v *Value) sizeContainer(name string, start bool) string {
	tmpl := `{{if .check}} if {{.ReceiverName}}.{{.fieldName}} == nil {
		{{.ReceiverName}}.{{.fieldName}} = new({{.structName}})
	}
	{{end}} {{ .dst }} += {{.ReceiverName}}.{{.fieldName}}.SizeSSZ()`

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
		"ReceiverName": v.ReceiverName(),
	})
}

func (v *Value) Size() string {
	return v.byteCounter("size")
}

func (v *Value) Offset() string {
	return v.byteCounter("offset")
}

// 'fieldName' is the fieldName of target variable we assign the size too. We also use this function
// during marshalling to figure out the size of the offset
func (v *Value) byteCounter(name string) string {
	if v.IsFixed() {
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
		return fmt.Sprintf(name+" += len(%s.%s)", v.ReceiverName(), v.fieldName)

	case TypeList:
		fallthrough

	case TypeVector:
		if v.elementType.IsFixed() {
			return fmt.Sprintf("%s += len(%s.%s) * %d", name, v.ReceiverName(), v.fieldName, v.elementType.valueSize)
		}
		v.elementType.fieldName = v.fieldName + "[ii]"
		tmpl := `for ii := 0; ii < len({{.ReceiverName}}.{{.fieldName}}); ii++ {
			{{.size}} += 4
			{{.dynamic}}
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName":    v.fieldName,
			"size":    name,
			"dynamic": v.elementType.Size(),
			"ReceiverName": v.ReceiverName(),
		})

	default:
		panic(fmt.Errorf("size not implemented for type %s", v.sszValueType.String()))
	}
}
