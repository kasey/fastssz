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
	// maxSize is the value from ssz-max annotation
	maxSize uint64
	// referencePackageAlias is the external reference if the struct is imported
	// from another package, eg:
	// import "ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	// type Foo struct {
	//   slashing ethpb.AttesterSlashing
	// } //       ^ ethpb would be the 'referencePackageAlias' field for the Value representing 'slashing'
	//
	referencePackageAlias string
	// new determines if the value is a pointer
	noPtr bool
	// When a value is contained by another Value as a field,
	// parentValue holds a reference to the containing Value
	parent *Value
	// fieldOffset is the position of a field within a container
	fieldOffset int
}

func (v *ValueRenderer) StructName() string {
	return v.structName
}

func (v *Value) isListElem() bool {
	return strings.HasSuffix(v.fieldName, "]")
}

func (v *Value) objRef() string {
	// global reference of the object including the package if the reference
	// is from an external package
	if v.referencePackageAlias == "" {
		return v.structName
	}
	return v.referencePackageAlias + "." + v.structName
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
				ref = i.referencePackageAlias
			}
		case TypeContainer:
			ref = i.referencePackageAlias
		case TypeList, TypeVector:
			ref = i.elementType.referencePackageAlias
		default:
			ref = i.referencePackageAlias
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

func (v *Value) parentRoot() *Value {
	// boundary case where v is already the parent
	//parent := v
	// when v.parent is nil, we are at the root
	/*
	for parent := v; parent != nil; parent = parent.parent {
	}
	return parent
	 */
	if v.parent == nil {
		return v
	}
	return v.parent.parentRoot()
}

func (v *Value) ReceiverName() string {
	root := v.parentRoot()
	return strings.ToLower(string(root.structName[0]))
}

func (v *ValueRenderer) ReceiverName() string {
	return v.Value.ReceiverName()
}

func (v *ValueRenderer) Fields() []*Value {
	return v.fields
}

func (v *ValueRenderer) HashTreeRoot() string {
	return v.Value.HashTreeRoot()
}