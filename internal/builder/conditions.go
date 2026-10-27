package builder

// Control visibility (Controls_Stack::is_control_visible and Conditions::check).

import (
	"regexp"
)

var condKeyRe = regexp.MustCompile(`(?i)^([a-z_\-0-9]+)(?:\[([a-z_]+)])?(!?)$`)

var condTermRe = regexp.MustCompile(`(\w+)(?:\[(\w+)])?`)

// isVisible mirrors Controls_Stack::is_control_visible.
func isVisible(c *ctrl, values map[string]any, controls map[string]*ctrl) bool {
	if c.Conditions != nil {
		if cm := asMap(c.Conditions); cm != nil && !checkConditions(cm, values) {
			return false
		}
	}
	if len(c.Condition) == 0 {
		return true
	}
	for key, condValue := range c.Condition {
		m := condKeyRe.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		pure, subKey, negative := m[1], m[2], m[3] == "!"
		v, ok := values[pure]
		if !ok || v == nil {
			return false
		}
		bothResponsive := c.Responsive != nil && controls[pure] != nil && controls[pure].hasResponsive()
		nameToCheck := pure
		var instance any
		if bothResponsive {
			suffix := c.responsiveSuffix()
			nameToCheck = pure + suffix
			if dv, ok := values[pure+suffix]; ok && dv != nil {
				instance = dv
			} else {
				instance = values[pure]
			}
		} else {
			instance = values[pure]
		}
		if subKey != "" && isArray(instance) {
			im := asMap(instance)
			sv, ok := im[subKey]
			if !ok || sv == nil {
				return false
			}
			instance = sv
		}
		if isEmpty(instance) {
			parent := ""
			if pc := controls[nameToCheck]; pc != nil {
				parent = pc.Parent
			}
			for parent != "" {
				instance = values[parent]
				if truthy(instance) {
					if !isArray(instance) {
						break
					}
					if subKey != "" {
						if sv, ok := asMap(instance)[subKey]; ok && sv != nil {
							instance = sv
							if !strictEq(instance, "") {
								break
							}
						}
					}
				}
				if pc := controls[parent]; pc != nil {
					parent = pc.Parent
				} else {
					parent = ""
				}
			}
		}
		var contains bool
		if l, ok := condValue.([]any); ok && len(l) > 0 {
			contains = inArrayStrict(instance, l)
		} else if cm, ok := condValue.(map[string]any); ok && len(cm) > 0 {
			contains = inArrayStrict(instance, cm)
		} else if isArray(instance) && truthy(instance) {
			contains = inArrayStrict(condValue, instance)
		} else {
			contains = strictEq(instance, condValue)
		}
		if (negative && contains) || (!negative && !contains) {
			return false
		}
	}
	return true
}

// checkConditions mirrors Conditions::check.
func checkConditions(conds map[string]any, values map[string]any) bool {
	isOr := phpString(conds["relation"]) == "or"
	succeed := !isOr
	for _, t := range asList(conds["terms"]) {
		term := asMap(t)
		if term == nil {
			continue
		}
		var result bool
		if truthy(term["terms"]) {
			result = checkConditions(term, values)
		} else {
			m := condTermRe.FindStringSubmatch(phpString(term["name"]))
			if m == nil {
				continue
			}
			value := values[m[1]]
			if m[2] != "" {
				value = sub(value, m[2])
			}
			result = compareCond(value, term["value"], phpString(term["operator"]))
		}
		if isOr {
			if result {
				return true
			}
		} else if !result {
			return false
		}
	}
	return succeed
}

func compareCond(left, right any, op string) bool {
	switch op {
	case "==":
		return looseEq(left, right)
	case "!=":
		return !looseEq(left, right)
	case "!==":
		return !strictEq(left, right)
	case "in":
		return inArrayStrict(left, right)
	case "!in":
		return !inArrayStrict(left, right)
	case "contains":
		return inArrayStrict(right, left)
	case "!contains":
		return !inArrayStrict(right, left)
	case "<":
		return toFloat(left) < toFloat(right)
	case "<=":
		return toFloat(left) <= toFloat(right)
	case ">":
		return toFloat(left) > toFloat(right)
	case ">=":
		return toFloat(left) >= toFloat(right)
	}
	return strictEq(left, right)
}

// activeSettings mirrors Controls_Stack::get_active_settings: values of hidden controls become nil.
func activeSettings(settings *omap, controls map[string]*ctrl) *omap {
	out := newOmap()
	for _, k := range settings.keys {
		v := settings.m[k]
		c, ok := controls[k]
		if !ok {
			out.Set(k, v)
			continue
		}
		if isVisible(c, settings.m, controls) {
			if c.isRepeater() {
				items := asList(v)
				nl := make([]any, len(items))
				for i, it := range items {
					im := asMap(it)
					if im == nil {
						nl[i] = it
						continue
					}
					io := newOmap()
					for _, f := range c.Fields {
						if _, ok := im[f.Name]; ok {
							io.Set(f.Name, im[f.Name])
						}
					}
					for _, ik := range sortedPlainKeys(im) {
						if !io.Has(ik) {
							io.Set(ik, im[ik])
						}
					}
					nl[i] = activeSettings(io, c.FieldsByKey).toMap()
				}
				out.Set(k, nl)
			} else {
				out.Set(k, v)
			}
		} else {
			out.Set(k, nil)
		}
	}
	return out
}

func (o *omap) toMap() map[string]any {
	m := make(map[string]any, len(o.m))
	for k, v := range o.m {
		m[k] = v
	}
	return m
}
