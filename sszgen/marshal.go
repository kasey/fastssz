package sszgen

import (
	"fmt"
	"strings"
)

// marshal creates a function that encodes the structs in SSZ format. It creates two functions:
// 1. MarshalTo(dst []byte) marshals the content to the target array.
// 2. Marshal() marshals the content to a newly created array.
func (e *env) marshal(name string, v *Value) string {
	tmpl := `// MarshalSSZ ssz marshals the {{.name}} object
	func (:: *{{.name}}) MarshalSSZ() ([]byte, error) {
		return ssz.MarshalSSZ(::)
	}

	// MarshalSSZTo ssz marshals the {{.name}} object to a target array
	func (:: *{{.name}}) MarshalSSZTo(buf []byte) (dst []byte, err error) {
		dst = buf
		{{.offset}}
		{{.marshal}}
		return
	}`

	data := map[string]interface{}{
		"name":    name,
		"marshal": v.marshalContainer(true),
		"offset":  "",
	}
	if !v.isFixed() {
		// offset is the position where the offset starts
		data["offset"] = fmt.Sprintf("offset := int(%d)\n", v.valueSize)
	}
	str := execTmpl(tmpl, data)
	return appendObjSignature(str, v)
}

func (v *Value) marshal() string {
	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return v.marshalContainer(false)

	case TypeBytes:
		name := v.fieldName
		if v.sizeIsVariable {
			name += "[:]"
		}
		tmpl := `{{.validate}}dst = append(dst, ::.{{.fieldName}}...)`

		return execTmpl(tmpl, map[string]interface{}{
			"validate": v.validate(),
			"fieldName":     name,
		})

	case TypeUint:
		var name string
		if v.referencePackageAlias != "" || v.structName != "" {
			// alias to Uint64
			name = fmt.Sprintf("uint64(::.%s)", v.fieldName)
		} else {
			name = "::." + v.fieldName
		}
		return fmt.Sprintf("dst = ssz.Marshal%s(dst, %s)", v.uintVToName(), name)

	case TypeBitList:
		return fmt.Sprintf("%sdst = append(dst, ::.%s...)", v.validate(), v.fieldName)

	case TypeBool:
		return fmt.Sprintf("dst = ssz.MarshalBool(dst, ::.%s)", v.fieldName)

	case TypeVector:
		if v.elementType.isFixed() {
			return v.marshalVector()
		}
		fallthrough

	case TypeList:
		return v.marshalList()

	default:
		panic(fmt.Errorf("marshal not implemented for type %s", v.sszValueType.String()))
	}
}

func (v *Value) marshalList() string {
	v.elementType.fieldName = v.fieldName + "[ii]"

	// bound check
	str := v.validate()

	if v.elementType.isFixed() {
		tmpl := `for ii := 0; ii < len(::.{{.fieldName}}); ii++ {
			{{.dynamic}}
		}`
		str += execTmpl(tmpl, map[string]interface{}{
			"fieldName":    v.fieldName,
			"dynamic": v.elementType.marshal(),
		})
		return str
	}

	// encode a list of dynamic objects:
	// 1. write offsets for each
	// 2. marshal each element

	tmpl := `{
		offset = 4 * len(::.{{.fieldName}})
		for ii := 0; ii < len(::.{{.fieldName}}); ii++ {
			dst = ssz.WriteOffset(dst, offset)
			{{.size}}
		}
	}
	for ii := 0; ii < len(::.{{.fieldName}}); ii++ {
		{{.marshal}}
	}`

	str += execTmpl(tmpl, map[string]interface{}{
		"fieldName":    v.fieldName,
		"size":    v.elementType.size("offset"),
		"marshal": v.elementType.marshal(),
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
		"marshal":  v.elementType.marshal(),
	})
}

func (v *Value) marshalContainer(start bool) string {
	if !start {
		tmpl := `{{ if .check }}if ::.{{.fieldName}} == nil {
			::.{{.fieldName}} = new({{.structName}})
		}
		{{ end }}if dst, err = ::.{{.fieldName}}.MarshalSSZTo(dst); err != nil {
			return
		}`
		// validate only for fixed structs
		check := v.isFixed()
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
		})
	}

	offset := v.valueSize
	out := []string{}

	for indx, i := range v.fields {
		var str string
		if i.isFixed() {
			// write the content
			str = fmt.Sprintf("// Field (%d) '%s'\n%s\n", indx, i.fieldName, i.marshal())
		} else {
			// write the offset
			str = fmt.Sprintf("// Offset (%d) '%s'\ndst = ssz.WriteOffset(dst, offset)\n%s\n", indx, i.fieldName, i.size("offset"))
			offset += i.valueSize
		}
		out = append(out, str)
	}

	// write the dynamic parts
	for indx, i := range v.fields {
		if !i.isFixed() {
			out = append(out, fmt.Sprintf("// Field (%d) '%s'\n%s\n", indx, i.fieldName, i.marshal()))
		}
	}
	return strings.Join(out, "\n")
}
