package sszgen

import (
	"fmt"
)

// marshal creates a function that encodes the structs in SSZ format. It creates two functions:
// 1. MarshalTo(dst []byte) marshals the content to the target array.
// 2. Marshal() marshals the content to a newly created array.
func (v *ValueRenderer) MarshalSSZ() string {
	tmpl := `// MarshalSSZ ssz marshals the {{.StructName}} object
	func ({{.ReceiverName}} *{{.StructName}}) MarshalSSZ() ([]byte, error) {
		return ssz.MarshalSSZ({{.ReceiverName}})
	}

	// MarshalSSZTo ssz marshals the {{.StructName}} object to a target array
	func ({{.ReceiverName}} *{{.StructName}}) MarshalSSZTo(buf []byte) (dst []byte, err error) {
		dst = buf
		{{ if not .IsFixed }}
		offset := int({{.ValueSize}})
		{{ end }}
		{{ range .Fields }}
			{{- if .IsFixed }}
		// Field ({{.FieldOffset}}) '{{.FieldName}}'
		{{.MarshalValue}}
			{{- else }}
		// Offset ({{.FieldOffset}}) '{{.FieldName}}'
		dst = ssz.WriteOffset(dst, offset)
		{{.Offset}}
			{{- end }}
		{{ end }}
		{{ range .Fields }}
			{{ if not .IsFixed }}
		// Field ({{.FieldOffset}}) '{{.FieldName}}'
		{{.MarshalValue}}
			{{ end }}
		{{ end }}
		return
	}`

	return execTmpl(tmpl, v)
}

func (v *Value) MarshalValue() string {
	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return v.marshalContainer()

	case TypeBytes:
		name := v.fieldName
		if v.sizeIsVariable {
			name += "[:]"
		}
		tmpl := `{{.validate}}dst = append(dst, {{.ReceiverName}}.{{.fieldName}}...)`

		return execTmpl(tmpl, map[string]interface{}{
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
		return fmt.Sprintf("dst = ssz.Marshal%s(dst, %s)", v.uintVToName(), name)

	case TypeBitList:
		return fmt.Sprintf("%sdst = append(dst, %s.%s...)", v.ReceiverName(), v.validate(), v.fieldName)

	case TypeBool:
		return fmt.Sprintf("dst = ssz.MarshalBool(dst, %s.%s)", v.ReceiverName(), v.fieldName)

	case TypeVector:
		if v.elementType.IsFixed() {
			return v.marshalVector()
		}
		fallthrough

	case TypeList:
		return v.marshalList()

	default:
		panic(fmt.Errorf("MarshalValue not implemented for type %s", v.sszValueType.String()))
	}
}

func (v *Value) marshalList() string {
	v.elementType.fieldName = v.fieldName + "[ii]"

	// bound check
	str := v.validate()

	if v.elementType.IsFixed() {
		tmpl := `for ii := 0; ii < len({{.ReceiverName}}.{{.fieldName}}); ii++ {
			{{.dynamic}}
		}`
		str += execTmpl(tmpl, map[string]interface{}{
			"fieldName":    v.fieldName,
			"dynamic": v.elementType.MarshalValue(),
			"ReceiverName": v.ReceiverName(),
		})
		return str
	}

	// encode a list of dynamic objects:
	// 1. write offsets for each
	// 2. marshal each element

	tmpl := `{
		offset = 4 * len({{.ReceiverName}}.{{.fieldName}})
		for ii := 0; ii < len({{.ReceiverName}}.{{.fieldName}}); ii++ {
			dst = ssz.WriteOffset(dst, offset)
			{{.size}}
		}
	}
	for ii := 0; ii < len({{.ReceiverName}}.{{.fieldName}}); ii++ {
		{{.marshal}}
	}`

	str += execTmpl(tmpl, map[string]interface{}{
		"fieldName":    v.fieldName,
		"size":    v.elementType.Offset(),
		"MarshalValue": v.elementType.MarshalValue(),
		"ReceiverName": v.ReceiverName(),
	})
	return str
}

func (v *Value) marshalVector() (str string) {
	v.elementType.fieldName = fmt.Sprintf("%s[ii]", v.fieldName)

	tmpl := `{{.validate}}for ii := 0; ii < {{.size}}; ii++ {
		{{.marshal}}
	}`
	return execTmpl(tmpl, map[string]interface{}{
		"validate": v.validate(),
		"fieldName":     v.fieldName,
		"size":     v.sizeInBytes,
		"MarshalValue":  v.elementType.MarshalValue(),
	})
}

func (v *Value) marshalContainer() string {
	tmpl := `{{ if .check }}if {{.ReceiverName}}.{{.fieldName}} == nil {
		{{.ReceiverName}}.{{.fieldName}} = new({{.structName}})
	}
	{{ end }}if dst, err = {{.ReceiverName}}.{{.fieldName}}.MarshalSSZTo(dst); err != nil {
		return
	}`
	// validate only for fixed structs
	check := v.IsFixed()
	if v.isListElem() {
		check = false
	}
	if v.noPtr {
		check = false
	}
	return execTmpl(tmpl, map[string]interface{}{
		"fieldName":  v.fieldName,
		"structName":   v.objRef(),
		"check": check,
		"ReceiverName": v.ReceiverName(),
	})
}
