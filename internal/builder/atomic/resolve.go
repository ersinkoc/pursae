package atomic

// The props resolver (props-resolver/render-props-resolver.php): walks a schema, takes each prop's
// saved value (or the schema default), and transforms {"$$type", "value"} values bottom-up with the
// transformer registered for the type, up to three levels deep. Transformers that return
// multiProps spread several CSS properties (padding → padding-block-start, ...).

// multiProps is Multi_Props::generate.
type multiProps struct{ m *Map }

type transformer func(r *resolver, value any, key string, pt *PropType) any

type resolver struct {
	env          *Env
	transformers map[string]transformer
}

func transformable(v any) (*Map, string, bool) {
	m, ok := v.(*Map)
	if !ok {
		return nil, "", false
	}
	t, _ := m.Get("$$type").(string)
	if t == "" || !m.Has("value") {
		return nil, "", false
	}
	return m, t, true
}

// resolve mirrors Render_Props_Resolver::resolve.
func (r *resolver) resolve(s *Schema, props *Map) *Map {
	out := NewMap()
	if s == nil {
		return out
	}
	for _, key := range s.Keys {
		pt := s.Props[key]
		v, _ := props.Lookup(key)
		if v == nil {
			v = pt.Default
		}
		t := r.resolveItem(v, key, pt, 0)
		if mp, ok := t.(multiProps); ok {
			for _, k := range mp.m.Keys() {
				out.Set(k, mp.m.Get(k))
			}
			continue
		}
		out.Set(key, t)
	}
	return out
}

func (r *resolver) resolveItem(v any, key string, pt *PropType, depth int) any {
	if v == nil {
		return nil
	}
	m, _, ok := transformable(v)
	if !ok {
		return v
	}
	if depth >= 3 {
		return nil
	}
	if m.Get("disabled") == true {
		return nil
	}
	t := r.transform(m, key, pt)
	if _, ok := t.(multiProps); ok {
		return t
	}
	return r.resolveItem(t, key, pt, depth+1)
}

func (r *resolver) transform(v *Map, key string, pt *PropType) any {
	typ, _ := v.Get("$$type").(string)
	if pt == nil {
		return nil
	}
	if pt.Kind == "union" {
		pt = pt.Types[typ]
		if pt == nil {
			return nil
		}
	}
	if typ != pt.Key {
		return nil
	}
	value := v.Get("value")
	switch pt.Kind {
	case "object":
		if !isArray(value) {
			return nil
		}
		value = r.resolve(pt.Shape, asMap(value))
	case "array":
		if !isArray(value) {
			return nil
		}
		items := []any{}
		for _, it := range listOf(value) {
			if res := r.resolveItem(it, "", pt.Item, 0); res != nil {
				items = append(items, res)
			}
		}
		value = items
	}
	f := r.transformers[typ]
	if f == nil {
		return value // Plain_Transformer
	}
	return f(r, value, key, pt)
}

// resolveValue resolves one value against one prop type (Render_Props_Resolver::resolve_value).
func (r *resolver) resolveValue(v any, pt *PropType) any {
	if v == nil {
		v = pt.Default
	}
	return r.resolveItem(v, "", pt, 0)
}
