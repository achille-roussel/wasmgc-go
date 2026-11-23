package wasm

import "fmt"

var magic = []byte{0x00, 0x61, 0x73, 0x6d}
var version = []byte{0x01, 0x00, 0x00, 0x00}

// Encode returns the binary encoding of m.
func (m *Module) Encode() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 256)
	buf = append(buf, magic...)
	buf = append(buf, version...)

	if len(m.Types) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Types)))
		for _, g := range m.Types {
			body = g.encode(body)
		}
		buf = appendSection(buf, SectionType, body)
	}

	if len(m.Imports) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Imports)))
		for _, im := range m.Imports {
			body = encodeName(body, im.Module)
			body = encodeName(body, im.Name)
			body = append(body, im.Kind)
			switch im.Kind {
			case ExtFunc, ExtTag:
				body = AppendU32(body, im.TypeIdx)
			case ExtTable:
				body = im.TableType.encode(body)
			case ExtMem:
				body = im.MemType.encode(body)
			case ExtGlobal:
				body = im.GlobalType.encode(body)
			default:
				return nil, fmt.Errorf("wasm: unknown import kind %#x", im.Kind)
			}
		}
		buf = appendSection(buf, SectionImport, body)
	}

	if len(m.Funcs) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Funcs)))
		for _, t := range m.Funcs {
			body = AppendU32(body, t)
		}
		buf = appendSection(buf, SectionFunction, body)
	}

	if len(m.Tables) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Tables)))
		for _, t := range m.Tables {
			body = t.encode(body)
		}
		buf = appendSection(buf, SectionTable, body)
	}

	if len(m.Memories) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Memories)))
		for _, lim := range m.Memories {
			body = lim.encode(body)
		}
		buf = appendSection(buf, SectionMemory, body)
	}

	if len(m.Tags) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Tags)))
		for _, tag := range m.Tags {
			body = append(body, 0x00) // exception attribute
			body = AppendU32(body, tag.TypeIdx)
		}
		buf = appendSection(buf, SectionTag, body)
	}

	if len(m.Globals) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Globals)))
		for _, g := range m.Globals {
			body = g.Type.encode(body)
			body = append(body, g.Init...)
			if len(g.Init) == 0 || g.Init[len(g.Init)-1] != OpEnd {
				body = append(body, OpEnd)
			}
		}
		buf = appendSection(buf, SectionGlobal, body)
	}

	if len(m.Exports) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Exports)))
		for _, e := range m.Exports {
			body = encodeName(body, e.Name)
			body = append(body, e.Kind)
			body = AppendU32(body, e.Index)
		}
		buf = appendSection(buf, SectionExport, body)
	}

	if m.Start != nil {
		var body []byte
		body = AppendU32(body, *m.Start)
		buf = appendSection(buf, SectionStart, body)
	}

	if len(m.Elements) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Elements)))
		for _, el := range m.Elements {
			var err error
			body, err = encodeElement(body, el)
			if err != nil {
				return nil, err
			}
		}
		buf = appendSection(buf, SectionElement, body)
	}

	emitDC := m.EmitDataCount || (m.dataCountSet && len(m.Datas) > 0)
	if emitDC && len(m.Datas) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Datas)))
		buf = appendSection(buf, SectionDataCount, body)
	}

	if len(m.Codes) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Codes)))
		for _, c := range m.Codes {
			var funcBody []byte
			funcBody = AppendU32(funcBody, uint32(len(c.Locals)))
			for _, loc := range c.Locals {
				funcBody = AppendU32(funcBody, loc.Count)
				funcBody = loc.Type.encode(funcBody)
			}
			funcBody = append(funcBody, c.Body...)
			// Function bodies are an expression terminated by end. Nested
			// block/loop/if/try_table ends are part of Body; this end closes
			// the function itself.
			funcBody = append(funcBody, OpEnd)
			body = AppendU32(body, uint32(len(funcBody)))
			body = append(body, funcBody...)
		}
		buf = appendSection(buf, SectionCode, body)
	}

	if len(m.Datas) > 0 {
		var body []byte
		body = AppendU32(body, uint32(len(m.Datas)))
		for _, d := range m.Datas {
			if d.Passive {
				body = append(body, 0x01)
			} else if d.MemIdx == 0 {
				body = append(body, 0x00)
				body = append(body, d.Offset...)
				if len(d.Offset) == 0 || d.Offset[len(d.Offset)-1] != OpEnd {
					body = append(body, OpEnd)
				}
			} else {
				body = append(body, 0x02)
				body = AppendU32(body, d.MemIdx)
				body = append(body, d.Offset...)
				if len(d.Offset) == 0 || d.Offset[len(d.Offset)-1] != OpEnd {
					body = append(body, OpEnd)
				}
			}
			body = AppendU32(body, uint32(len(d.Bytes)))
			body = append(body, d.Bytes...)
		}
		buf = appendSection(buf, SectionData, body)
	}

	if m.Names != nil {
		nameBody := encodeNameSection(m.Names)
		buf = appendCustomSection(buf, "name", nameBody)
	}

	return buf, nil
}

func appendSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = AppendU32(buf, uint32(len(body)))
	return append(buf, body...)
}

func appendCustomSection(buf []byte, name string, body []byte) []byte {
	var content []byte
	content = encodeName(content, name)
	content = append(content, body...)
	return appendSection(buf, SectionCustom, content)
}

func encodeName(buf []byte, s string) []byte {
	buf = AppendU32(buf, uint32(len(s)))
	return append(buf, s...)
}

// Element segment binary forms (MVP + bulk-memory / ref-types):
//
//	0x00  offset  vec(funcidx)                          active funcref, table 0
//	0x01  elemkind  vec(funcidx)                        passive
//	0x02  tableidx  offset  elemkind  vec(funcidx)      active
//	0x03  elemkind  vec(funcidx)                        declarative
//	0x04  offset  vec(expr)                             active funcref exprs
//	0x05  reftype  vec(expr)                            passive
//	0x06  tableidx  offset  reftype  vec(expr)          active
//	0x07  reftype  vec(expr)                            declarative
func encodeElement(buf []byte, el Element) ([]byte, error) {
	useExprs := el.Exprs != nil
	switch el.Mode {
	case ElemActive:
		if el.TableIdx == 0 && !el.HasType && !useExprs {
			buf = append(buf, 0x00)
			buf = append(buf, el.Offset...)
			if len(el.Offset) == 0 || el.Offset[len(el.Offset)-1] != OpEnd {
				buf = append(buf, OpEnd)
			}
			buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
			for _, f := range el.FuncIdxs {
				buf = AppendU32(buf, f)
			}
			return buf, nil
		}
		if useExprs || el.HasType {
			if el.TableIdx == 0 && !el.HasType {
				buf = append(buf, 0x04)
				buf = append(buf, el.Offset...)
				if len(el.Offset) == 0 || el.Offset[len(el.Offset)-1] != OpEnd {
					buf = append(buf, OpEnd)
				}
			} else {
				buf = append(buf, 0x06)
				buf = AppendU32(buf, el.TableIdx)
				buf = append(buf, el.Offset...)
				if len(el.Offset) == 0 || el.Offset[len(el.Offset)-1] != OpEnd {
					buf = append(buf, OpEnd)
				}
				buf = el.Type.encode(buf)
			}
			return encodeElemExprs(buf, el)
		}
		buf = append(buf, 0x02)
		buf = AppendU32(buf, el.TableIdx)
		buf = append(buf, el.Offset...)
		if len(el.Offset) == 0 || el.Offset[len(el.Offset)-1] != OpEnd {
			buf = append(buf, OpEnd)
		}
		buf = append(buf, 0x00) // elemkind funcref
		buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
		for _, f := range el.FuncIdxs {
			buf = AppendU32(buf, f)
		}
		return buf, nil

	case ElemPassive:
		if useExprs || el.HasType {
			buf = append(buf, 0x05)
			buf = el.Type.encode(buf)
			return encodeElemExprs(buf, el)
		}
		buf = append(buf, 0x01)
		buf = append(buf, 0x00)
		buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
		for _, f := range el.FuncIdxs {
			buf = AppendU32(buf, f)
		}
		return buf, nil

	case ElemDeclarative:
		if useExprs || el.HasType {
			buf = append(buf, 0x07)
			buf = el.Type.encode(buf)
			if useExprs {
				return encodeElemExprs(buf, el)
			}
			// Declarative with type + funcidxs: encode as ref.func exprs.
			buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
			for _, f := range el.FuncIdxs {
				buf = append(buf, OpRefFunc)
				buf = AppendU32(buf, f)
				buf = append(buf, OpEnd)
			}
			return buf, nil
		}
		buf = append(buf, 0x03)
		buf = append(buf, 0x00)
		buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
		for _, f := range el.FuncIdxs {
			buf = AppendU32(buf, f)
		}
		return buf, nil
	}
	return nil, fmt.Errorf("wasm: unknown element mode %d", el.Mode)
}

func encodeElemExprs(buf []byte, el Element) ([]byte, error) {
	if el.Exprs != nil {
		buf = AppendU32(buf, uint32(len(el.Exprs)))
		for _, e := range el.Exprs {
			buf = append(buf, e...)
			if len(e) == 0 || e[len(e)-1] != OpEnd {
				buf = append(buf, OpEnd)
			}
		}
		return buf, nil
	}
	buf = AppendU32(buf, uint32(len(el.FuncIdxs)))
	for _, f := range el.FuncIdxs {
		buf = append(buf, OpRefFunc)
		buf = AppendU32(buf, f)
		buf = append(buf, OpEnd)
	}
	return buf, nil
}

func encodeNameSection(ns *NameSection) []byte {
	var buf []byte
	if ns.ModuleName != "" {
		var sub []byte
		sub = encodeName(sub, ns.ModuleName)
		buf = append(buf, 0x00) // module name subsection
		buf = AppendU32(buf, uint32(len(sub)))
		buf = append(buf, sub...)
	}
	if len(ns.Functions) > 0 {
		var sub []byte
		sub = AppendU32(sub, uint32(len(ns.Functions)))
		for _, fn := range ns.Functions {
			sub = AppendU32(sub, fn.Index)
			sub = encodeName(sub, fn.Name)
		}
		buf = append(buf, 0x01)
		buf = AppendU32(buf, uint32(len(sub)))
		buf = append(buf, sub...)
	}
	if len(ns.Locals) > 0 {
		// Group by function index.
		byFunc := map[uint32][]LocalName{}
		var order []uint32
		for _, ln := range ns.Locals {
			if _, ok := byFunc[ln.FuncIndex]; !ok {
				order = append(order, ln.FuncIndex)
			}
			byFunc[ln.FuncIndex] = append(byFunc[ln.FuncIndex], ln)
		}
		var sub []byte
		sub = AppendU32(sub, uint32(len(order)))
		for _, fi := range order {
			locs := byFunc[fi]
			sub = AppendU32(sub, fi)
			sub = AppendU32(sub, uint32(len(locs)))
			for _, ln := range locs {
				sub = AppendU32(sub, ln.LocalIndex)
				sub = encodeName(sub, ln.Name)
			}
		}
		buf = append(buf, 0x02)
		buf = AppendU32(buf, uint32(len(sub)))
		buf = append(buf, sub...)
	}
	if len(ns.Types) > 0 {
		var sub []byte
		sub = AppendU32(sub, uint32(len(ns.Types)))
		for _, tn := range ns.Types {
			sub = AppendU32(sub, tn.Index)
			sub = encodeName(sub, tn.Name)
		}
		buf = append(buf, 0x04) // type names subsection (extended name section)
		buf = AppendU32(buf, uint32(len(sub)))
		buf = append(buf, sub...)
	}
	return buf
}
