// Things not yet implemented:
// - enforcing use="restricted" on attributes
// - namespaces

package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

var (
	usage = `Usage:

  goxsd <xsd>

Arguments:

  xsd     Path to a valid XSD file

goxsd is a tool for generating XML decoding Go structs, according to an XSD
schema.

The argument is expected to be the path to a valid XSD schema file. Any import
statements in that file will be be followed and parsed. The resulting set of
Go structs will be printed on stdout.
`
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println(usage)
		os.Exit(1)
	}
	xsdFile := os.Args[1]

	s, err := extractSchemas(xsdFile)
	if err != nil {
		log.Fatal(err)
	}
	builder := newBuilder(s)
	parse(os.Stdout, builder.buildXML())
}

type xmlElem struct {
	Name     string
	Type     string
	List     bool
	Cdata    bool
	Attribs  []xmlAttrib
	Children []*xmlElem
}

type xmlAttrib struct {
	Name string
	Type string
}

type builder struct {
	schemas    []xsdSchema
	complTypes map[string]xsdComplexType
	simplTypes map[string]xsdSimpleType
}

func newBuilder(s []xsdSchema) builder {
	return builder{
		schemas:    s,
		complTypes: make(map[string]xsdComplexType),
		simplTypes: make(map[string]xsdSimpleType),
	}
}

func (b builder) buildXML() []*xmlElem {
	var roots []xsdElement
	for _, s := range b.schemas {
		for _, e := range s.Elements {
			roots = append(roots, e)
		}
		for _, t := range s.ComplexTypes {
			b.complTypes[t.Name] = t
		}
		for _, t := range s.SimpleTypes {
			b.simplTypes[t.Name] = t
		}
	}

	var xelems []*xmlElem
	for _, e := range roots {
		xelems = append(xelems, b.buildFromElement(e))
	}

	return xelems
}

// Build a xmlElem from an xsdElement, recursively traverse the XSD type
// information to build up a XML descendant hierarchy.
func (b builder) buildFromElement(e xsdElement) *xmlElem {
	xelem := &xmlElem{Name: e.Name, Type: e.Name}

	if e.isList() {
		xelem.List = true
	}

	if !e.inlineType() {
		switch t := b.findType(e.Type).(type) {
		case xsdComplexType:
			b.buildFromComplexType(xelem, t)
		case xsdSimpleType:
			buildFromSimpleType(xelem, t)
		case string:
			xelem.Type = t
		}
		return xelem
	}

	if e.ComplexType != nil { // inline complex type
		b.buildFromComplexType(xelem, *e.ComplexType)
		return xelem
	}

	if e.SimpleType != nil { // inline simple type
		buildFromSimpleType(xelem, *e.SimpleType)
		return xelem
	}

	return xelem
}

func (b builder) buildFromComplexContent(xelem *xmlElem, c xsdComplexContent) {
	if c.Extension != nil {
		if c.Extension.Sequence != nil {
			for _, e := range c.Extension.Sequence {
				xelem.Children = append(xelem.Children, b.buildFromElement(e))
			}
		}
		base := c.Extension.Base
		switch t := b.findType(base).(type) {
		case xsdComplexType:
			b.buildFromComplexType(xelem, t)
		}

	}
}

func typeFromXsdType(typ string) string {
	switch typ {
	case "boolean":
		typ = "bool"
	case "language", "dateTime", "Name", "token":
		typ = "string"
	case "long", "short", "integer", "int":
		typ = "int"
	case "decimal":
		typ = "float64"
	}
	return typ
}

func addAttributes(xelem *xmlElem, attribs []xsdAttribute) {
	if attribs != nil {
		for _, attr := range attribs {
			typ := typeFromXsdType(stripNamespace(attr.Type))
			xelem.Attribs = append(xelem.Attribs, xmlAttrib{Name: attr.Name, Type: typ})
		}
	}
}

// A simple content can refer to a text-only complex type
func (b builder) buildFromSimpleContent(xelem *xmlElem, c xsdSimpleContent) {
	if c.Extension != nil {
		// (annotation?, ((group|all|choice|sequence)?, ((attribute|attributeGroup)*, anyAttribute?)))
		if c.Extension.Attributes != nil {
			b.buildFromAttributes(xelem, c.Extension.Attributes)
		}
		// has always a base type

		var child *xmlElem
		switch t := b.findType(c.Extension.Base).(type) {
		case xsdComplexType:
			b.buildFromComplexType(xelem, t)
		case xsdSimpleType:
			child = &xmlElem{Name: xelem.Name, Cdata: true}
			buildFromSimpleType(child, t)
			xelem.Children = []*xmlElem{child}
		default:
			child = &xmlElem{Name: xelem.Name, Cdata: true}
			child.Type = typeFromXsdType(t.(string))
			xelem.Children = []*xmlElem{child}
		}
	}

	if c.Restriction != nil {
		switch t := b.findType(c.Restriction.Base).(type) {
		case xsdComplexType:
			b.buildFromComplexType(xelem, t)
		case xsdSimpleType:
			buildFromSimpleType(xelem, t)
		default:
			xelem.Type = typeFromXsdType(t.(string))
			//addAttributes(xelem, c.Extension.Attributes)
		}
	}
}

func (b builder) buildFromAttributes(xelem *xmlElem, attrs []xsdAttribute) {
	for _, a := range attrs {
		attr := xmlAttrib{Name: a.Name}
		switch t := b.findType(a.Type).(type) {
		case xsdSimpleType:
			// Get type name from simpleType
			// If Restriction.Base is a simpleType or complexType, we panic
			attr.Type = b.findType(t.Restriction.Base).(string)
		case string:
			// If empty, then simpleType is present as content, but we ignore
			// that now
			attr.Type = t
		}
		xelem.Attribs = append(xelem.Attribs, attr)
	}
}

// buildFromComplexType takes an xmlElem and an xsdComplexType, containing
// XSD type information for xmlElem enrichment.
func (b builder) buildFromComplexType(xelem *xmlElem, t xsdComplexType) {
	if t.Sequence != nil { // Does the element have children?
		for _, e := range t.Sequence {
			xelem.Children = append(xelem.Children, b.buildFromElement(e))
		}
	}

	if t.Attributes != nil {
		b.buildFromAttributes(xelem, t.Attributes)
	}

	if t.ComplexContent != nil {
		b.buildFromComplexContent(xelem, *t.ComplexContent)
	}

	if t.SimpleContent != nil {
		b.buildFromSimpleContent(xelem, *t.SimpleContent)
	}
}

func buildFromSimpleType(xelem *xmlElem, t xsdSimpleType) {
	xelem.Type = typeFromXsdType(stripNamespace(t.Restriction.Base))
}

// findType takes a type name and checks if it is a registered XSD type
// (simple or complex), in which case that type is returned. If no such
// type can be found, the XSD specific primitive types are mapped to their
// Go correspondents. If no XSD type was found, the type name itself is
// returned.
func (b builder) findType(name string) interface{} {
	name = stripNamespace(name)
	if t, ok := b.complTypes[name]; ok {
		return t
	}
	if t, ok := b.simplTypes[name]; ok {
		return t
	}

	switch name {
	case "boolean":
		return "bool"
	case "language", "dateTime", "Name", "token":
		return "string"
	case "long", "short", "integer", "int":
		return "int"
	case "decimal":
		return "float64"
	default:
		return name
	}
}

func stripNamespace(name string) string {
	if s := strings.Split(name, ":"); len(s) > 1 {
		return s[len(s)-1]
	}
	return name
}
