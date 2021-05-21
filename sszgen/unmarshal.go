package sszgen

import (
	"fmt"
	"strconv"
	"strings"
)

// unmarshal creates a function that decodes the structs with the input byte in SSZ format.
func (v *ValueRenderer) UnmarshalSSZ() string {
	tmpl := `// UnmarshalSSZ ssz unmarshals the {{.name}} object
	func ({{.ReceiverName}} *{{.name}}) UnmarshalSSZ(buf []byte) error {
		var err error
		{{.unmarshal}}
		return err
	}`

	return execTmpl(tmpl, map[string]interface{}{
		"name":      v.StructName(),
		"unmarshal": v.umarshalContainer(true, "buf"),
		"ReceiverName": v.ReceiverName(),
	})
}

func (v *Value) unmarshal(dst string) string {
	// we use dst as the input buffer where the SSZ data to decode the value is.
	switch v.sszValueType {
	case TypeContainer, TypeReference:
		return v.umarshalContainer(false, dst)

	case TypeBytes:
		if v.sizeIsVariable {
			return fmt.Sprintf("copy(%s.%s[:], %s)", v.ReceiverName(), v.fieldName, dst)
		}
		validate := ""
		if v.sizeInBytes == 0 {
			// dynamic bytes, we need to validate the size of the buffer
			validate = fmt.Sprintf("if len(%s) > %d { return ssz.ErrBytesLength }\n", dst, v.maxSize)
		}
		// both fixed and dynamic are decoded equally
		tmpl := `{{.validate}}if cap({{.ReceiverName}}.{{.fieldName}}) == 0 {
			{{.ReceiverName}}.{{.fieldName}} = make([]byte, 0, len({{.dst}}))
		}
		{{.ReceiverName}}.{{.fieldName}} = append({{.ReceiverName}}.{{.fieldName}}, {{.dst}}...)`
		return execTmpl(tmpl, map[string]interface{}{
			"validate": validate,
			"fieldName":     v.fieldName,
			"dst":      dst,
			"size":     v.maxSize,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeUint:
		if v.referencePackageAlias != "" {
			// alias, we need to cast the value
			return fmt.Sprintf("%s.%s = %s.%s(ssz.Unmarshall%s(%s))", v.ReceiverName(), v.fieldName, v.referencePackageAlias, v.structName, v.uintVToName(), dst)
		}
		if v.structName != "" {
			// alias to a type on the same package
			return fmt.Sprintf("%s.%s = %s(ssz.Unmarshall%s(%s))", v.ReceiverName(), v.fieldName, v.structName, v.uintVToName(), dst)
		}
		return fmt.Sprintf("%s.%s = ssz.Unmarshall%s(%s)", v.ReceiverName(), v.fieldName, v.uintVToName(), dst)

	case TypeBitList:
		tmpl := `if err = ssz.ValidateBitlist({{.dst}}, {{.size}}); err != nil {
			return err
		}
		if cap({{.ReceiverName}}.{{.fieldName}}) == 0 {
			{{.ReceiverName}}.{{.fieldName}} = make([]byte, 0, len({{.dst}}))
		}
		{{.ReceiverName}}.{{.fieldName}} = append({{.ReceiverName}}.{{.fieldName}}, {{.dst}}...)`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"dst":  dst,
			"size": v.maxSize,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeVector:
		if v.elementType.IsFixed() {
			dst = fmt.Sprintf("%s[ii*%d: (ii+1)*%d]", dst, v.elementType.valueSize, v.elementType.valueSize)

			tmpl := `{{.create}}
			for ii := 0; ii < {{.size}}; ii++ {
				{{.unmarshal}}
			}`
			return execTmpl(tmpl, map[string]interface{}{
				"create":    v.createSlice(),
				"size":      v.sizeInBytes,
				"unmarshal": v.elementType.unmarshal(dst),
			})
		}
		fallthrough

	case TypeList:
		return v.unmarshalList()

	case TypeBool:
		return fmt.Sprintf("%s.%s = ssz.UnmarshalBool(%s)", v.ReceiverName(), v.fieldName, dst)

	default:
		panic(fmt.Errorf("unmarshal not implemented for type %d", v.sszValueType))
	}
}

func (v *Value) unmarshalList() string {

	// The Go field must have a 'ssz-max' tag to set the maximum number of items
	maxSize := v.sizeInBytes

	// In order to use createSlice with a dynamic list we need to set v.sizeInBytes to 0
	v.sizeInBytes = 0

	if v.elementType.IsFixed() {
		dst := fmt.Sprintf("buf[ii*%d: (ii+1)*%d]", v.elementType.valueSize, v.elementType.valueSize)

		tmpl := `num, err := ssz.DivideInt2(len(buf), {{.size}}, {{.max}})
		if err != nil {
			return err
		}
		{{.create}}
		for ii := 0; ii < num; ii++ {
			{{.unmarshal}}
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"size":      v.elementType.valueSize,
			"max":       maxSize,
			"create":    v.createSlice(),
			"unmarshal": v.elementType.unmarshal(dst),
		})
	}

	if v.sszValueType == TypeVector {
		panic("it cannot happen")
	}

	// Decode list with a dynamic element. 'ssz.DecodeDynamicLength' ensures
	// that the number of elements do not surpass the 'ssz-max' tag.

	tmpl := `num, err := ssz.DecodeDynamicLength(buf, {{.size}})
	if err != nil {
		return err
	}
	{{.create}}
	err = ssz.UnmarshalDynamic(buf, num, func(indx int, buf []byte) (err error) {
		{{.unmarshal}}
		return nil
	})
	if err != nil {
		return err
	}`

	v.elementType.fieldName = v.fieldName + "[indx]"

	data := map[string]interface{}{
		"size":      maxSize,
		"create":    v.createSlice(),
		"unmarshal": v.elementType.unmarshal("buf"),
	}
	return execTmpl(tmpl, data)
}

func (v *Value) umarshalContainer(start bool, dst string) (str string) {
	if !start {
		tmpl := `{{ if .check }}if {{.ReceiverName}}.{{.fieldName}} == nil {
			{{.ReceiverName}}.{{.fieldName}} = new({{.structName}})
		}
		{{ end }}if err = {{.ReceiverName}}.{{.fieldName}}.UnmarshalSSZ({{.dst}}); err != nil {
			return err
		}`
		check := true
		if v.noPtr {
			check = false
		}
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName":  v.fieldName,
			"structName":   v.objRef(),
			"dst":   dst,
			"check": check,
			"ReceiverName": v.ReceiverName(),
		})
	}

	var offsets []string
	offsetsMatch := map[string]string{}

	for indx, i := range v.fields {
		if !i.IsFixed() {
			name := "o" + strconv.Itoa(indx)
			if len(offsets) != 0 {
				offsetsMatch[name] = offsets[len(offsets)-1]
			}
			offsets = append(offsets, name)
		}
	}

	// safe check for the size. Two cases:
	// 1. Struct is fixed: The size of the input buffer must be the same as the struct.
	// 2. Struct is dynamic. The size of the input buffer must be higher than the fixed part of the struct.

	var cmp string
	if v.IsFixed() {
		cmp = "!="
	} else {
		cmp = "<"
	}

	// If the struct is dynamic we create a set of offset variables that will be readed later.

	tmpl := `size := uint64(len(buf))
	if size {{.cmp}} {{.size}} {
		return ssz.ErrSize
	}
	{{if .offsets}}
		tail := buf
		var {{.offsets}} uint64
	{{end}}
	`

	str += execTmpl(tmpl, map[string]interface{}{
		"cmp":     cmp,
		"size":    v.valueSize,
		"offsets": strings.Join(offsets, ", "),
	})

	var o0 uint64

	// Marshal the fixed part and offsets

	outs := []string{}
	for indx, i := range v.fields {

		// How much it increases on every item
		var incr uint64
		if i.IsFixed() {
			incr = i.valueSize
		} else {
			incr = 4
		}

		dst = fmt.Sprintf("%s[%d:%d]", "buf", o0, o0+incr)
		o0 += incr

		var res string
		if i.IsFixed() {
			res = fmt.Sprintf("// Field (%d) '%s'\n%s\n\n", indx, i.fieldName, i.unmarshal(dst))

		} else {
			// read the offset
			offset := "o" + strconv.Itoa(indx)

			data := map[string]interface{}{
				"indx":   indx,
				"fieldName":   i.fieldName,
				"offset": offset,
				"dst":    dst,
			}

			// We need to do two validations for the offset:
			// 1. The offset is lower than the total size of the input buffer
			// 2. The offset i needs to be higher than the offset i-1 (Only if the offset is not the first).

			if prev, ok := offsetsMatch[offset]; ok {
				data["more"] = fmt.Sprintf(" || %s > %s", prev, offset)
			} else {
				data["more"] = ""
			}

			tmpl := `// Offset ({{.indx}}) '{{.fieldName}}'
			if {{.offset}} = ssz.ReadOffset({{.dst}}); {{.offset}} > size {{.more}} {
				return ssz.ErrOffset
			}
			`
			res = execTmpl(tmpl, data)
		}
		outs = append(outs, res)
	}

	// Marshal the dynamic parts

	c := 0
	for indx, i := range v.fields {
		if !i.IsFixed() {

			from := offsets[c]
			var to string
			if c == len(offsets)-1 {
				to = ""
			} else {
				to = offsets[c+1]
			}

			tmpl := `// Field ({{.indx}}) '{{.fieldName}}'
			{
				buf = tail[{{.from}}:{{.to}}]
				{{.unmarshal}}
			}`
			res := execTmpl(tmpl, map[string]interface{}{
				"indx":      indx,
				"fieldName":      i.fieldName,
				"from":      from,
				"to":        to,
				"unmarshal": i.unmarshal("buf"),
			})
			outs = append(outs, res)
			c++
		}
	}

	str += strings.Join(outs, "\n\n")
	return
}

// createItem is used to initialize slices of objects
func (v *Value) createSlice() string {
	if v.sszValueType != TypeVector && v.sszValueType != TypeList {
		panic("BUG: create item is only intended to be used with vectors and lists")
	}

	// If v.sizeInBytes is set (fixed slice) we use that value, otherwise (variable size)
	// we assume there is a 'num' variable generated beforehand with the expected size.
	var size string
	if v.sizeInBytes == 0 {
		size = "num"
	} else {
		size = strconv.Itoa(int(v.sizeInBytes))
	}

	switch v.elementType.sszValueType {
	case TypeUint:
		// []int uses the Extend functions in the fastssz package
		return fmt.Sprintf("%s.%s = ssz.Extend%s(%s.%s, %s)", v.ReceiverName(), v.fieldName, v.elementType.uintVToName(), v.ReceiverName(), v.fieldName, size)

	case TypeContainer:
		// []*(referencePackageAlias.)Struct{}
		return fmt.Sprintf("%s.%s = make([]*%s, %s)", v.ReceiverName(), v.fieldName, v.elementType.objRef(), size)

	case TypeBytes:
		// [][]byte
		if v.sizeIsVariable {
			return ""
		}
		if v.elementType.sizeIsVariable {
			return fmt.Sprintf("%s.%s = make([][%d]byte, %s)", v.ReceiverName(), v.fieldName, v.elementType.sizeInBytes, size)
		}
		return fmt.Sprintf("%s.%s = make([][]byte, %s)", v.ReceiverName(), v.fieldName, size)

	default:
		panic(fmt.Sprintf("create not implemented for type %s", v.elementType.sszValueType.String()))
	}
}
