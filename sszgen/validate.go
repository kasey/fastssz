package sszgen

func (v *Value) validate() string {
	switch v.sszValueType {
	case TypeBitList, TypeBytes:
		cmp := "!="
		if v.sszValueType == TypeBitList {
			cmp = ">"
		}
		if v.sizeIsVariable {
			return ""
		}
		// fixed []byte
		size := v.sizeInBytes
		if size == 0 {
			// dynamic []byte
			size = v.maxSize
			cmp = ">"
		}

		tmpl := `if len({{.ReceiverName}}.{{.fieldName}}) {{.cmp}} {{.size}} {
			err = ssz.ErrBytesLength
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"cmp":  cmp,
			"fieldName": v.fieldName,
			"size": size,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeVector:
		if v.sizeIsVariable {
			return ""
		}
		// We only have vectors for [][]byte roots
		tmpl := `if len({{.ReceiverName}}.{{.fieldName}}) != {{.size}} {
			err = ssz.ErrVectorLength
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.sizeInBytes,
			"ReceiverName": v.ReceiverName(),
		})

	case TypeList:
		tmpl := `if len({{.ReceiverName}}.{{.fieldName}}) > {{.size}} {
			err = ssz.ErrListTooBig
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.sizeInBytes,
			"ReceiverName": v.ReceiverName(),
		})

	default:
		return ""
	}
}
