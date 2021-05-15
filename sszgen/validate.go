package sszgen

func (v *Value) validate() string {
	switch v.sszValueType {
	case TypeBitList, TypeBytes:
		cmp := "!="
		if v.sszValueType == TypeBitList {
			cmp = ">"
		}
		if v.c {
			return ""
		}
		// fixed []byte
		size := v.s
		if size == 0 {
			// dynamic []byte
			size = v.m
			cmp = ">"
		}

		tmpl := `if len(::.{{.fieldName}}) {{.cmp}} {{.size}} {
			err = ssz.ErrBytesLength
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"cmp":  cmp,
			"fieldName": v.fieldName,
			"size": size,
		})

	case TypeVector:
		if v.c {
			return ""
		}
		// We only have vectors for [][]byte roots
		tmpl := `if len(::.{{.fieldName}}) != {{.size}} {
			err = ssz.ErrVectorLength
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.s,
		})

	case TypeList:
		tmpl := `if len(::.{{.fieldName}}) > {{.size}} {
			err = ssz.ErrListTooBig
			return
		}
		`
		return execTmpl(tmpl, map[string]interface{}{
			"fieldName": v.fieldName,
			"size": v.s,
		})

	default:
		return ""
	}
}
