package sszgen

import (
	"fmt"
	"strings"
)

// Value is a type that represents a Go field or struct and his
// correspondent SSZ type.
type Value struct {
	// fieldName of the variable this value represents
	fieldName string
	// fieldName of the Go object this value represents
	structName string
	// valueSize is the fixed size of the value
	valueSize uint64
	// auxiliary int number
	sizeInBytes uint64
	// type of the value
	sszValueType Type
	// fields (for a struct type)
	fields []*Value
	// type of elements (for a vector/list type)
	elementType *Value
	// auxiliary boolean
	sizeIsVariable bool
	// another auxiliary int number
	m uint64
	// ref is the external reference if the struct is imported
	// from another package
	ref string
	// new determines if the value is a pointer
	noPtr bool
}

func (v *Value) isListElem() bool {
	return strings.HasSuffix(v.fieldName, "]")
}

func (v *Value) objRef() string {
	// global reference of the object including the package if the reference
	// is from an external package
	if v.ref == "" {
		return v.structName
	}
	return v.ref + "." + v.structName
}

func (v *Value) copy() *Value {
	vv := new(Value)
	*vv = *v
	vv.fields = make([]*Value, len(v.fields))
	for indx := range v.fields {
		vv.fields[indx] = v.fields[indx].copy()
	}
	if v.elementType != nil {
		vv.elementType = v.elementType.copy()
	}
	return vv
}

func (v *Value) isFixed() bool {
	switch v.sszValueType {
	case TypeVector:
		return v.elementType.isFixed()

	case TypeBytes:
		if v.sizeInBytes != 0 {
			// fixed bytes
			return true
		}
		// dynamic bytes
		return false

	case TypeContainer:
		return !v.sizeIsVariable

	// Dynamic types
	case TypeBitList:
		fallthrough
	case TypeList:
		return false

	// Fixed types
	case TypeBitVector:
		fallthrough
	case TypeUint:
		fallthrough
	case TypeBool:
		return true

	case TypeReference:
		if v.sizeInBytes != 0 {
			return true
		}
		return false

	default:
		panic(fmt.Errorf("is fixed not implemented for type %s", v.sszValueType.String()))
	}
}

func (v *Value) detectImports() []string {
	// for sure v is a container
	// check if any of the fields in the container has an import
	refs := []string{}
	for _, i := range v.fields {
		var ref string
		switch i.sszValueType {
		case TypeReference:
			if !i.noPtr {
				// it is not a typed reference
				ref = i.ref
			}
		case TypeContainer:
			ref = i.ref
		case TypeList, TypeVector:
			ref = i.elementType.ref
		default:
			ref = i.ref
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (v *Value) isBasicType() bool {
	return v.sszValueType == TypeUint || v.sszValueType == TypeBool || v.sszValueType == TypeBytes
}

func (v *Value) uintVToName() string {
	if v.sszValueType != TypeUint {
		panic("not expected")
	}
	switch v.valueSize {
	case 8:
		return "Uint64"
	case 4:
		return "Uint32"
	case 2:
		return "Uint16"
	case 1:
		return "Uint8"
	default:
		panic("not found")
	}
}
