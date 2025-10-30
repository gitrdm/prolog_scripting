package engine

import (
	"reflect"
)

// compare two procedure maps for semantic equality (ignores internal atomic pointer addresses)
func proceduresEqual(exp, act map[procedureIndicator]procedure) bool {
	if len(exp) != len(act) {
		return false
	}
	for k, v := range exp {
		av, ok := act[k]
		if !ok {
			return false
		}
		ev, eok := v.(*userDefined)
		avd, aok := av.(*userDefined)
		if eok && aok {
			if ev.public != avd.public || ev.dynamic != avd.dynamic || ev.multifile != avd.multifile || ev.discontiguous != avd.discontiguous {
				return false
			}
			ecs := ev.getClauses()
			acs := avd.getClauses()
			if len(ecs) != len(acs) {
				return false
			}
			for i := range ecs {
				ecl := ecs[i]
				acl := acs[i]
				if ecl.pi != acl.pi {
					return false
				}
				if !reflect.DeepEqual(ecl.raw, acl.raw) {
					return false
				}
				if !reflect.DeepEqual(ecl.bytecode, acl.bytecode) {
					return false
				}
				if !reflect.DeepEqual(ecl.vars, acl.vars) {
					return false
				}
			}
			continue
		}
		if !reflect.DeepEqual(v, av) {
			return false
		}
	}
	return true
}

// compare two procedure values for semantic equality (ignores atomic internals)
func procedureEqual(exp, act procedure) bool {
	if exp == nil && act == nil {
		return true
	}
	if exp == nil || act == nil {
		return false
	}
	ev, eok := exp.(*userDefined)
	av, aok := act.(*userDefined)
	if eok && aok {
		if ev.public != av.public || ev.dynamic != av.dynamic || ev.multifile != av.multifile || ev.discontiguous != av.discontiguous {
			return false
		}
		ecs := ev.getClauses()
		acs := av.getClauses()
		if len(ecs) != len(acs) {
			return false
		}
		for i := range ecs {
			ecl := ecs[i]
			acl := acs[i]
			if ecl.pi != acl.pi {
				return false
			}
			if !reflect.DeepEqual(ecl.raw, acl.raw) {
				return false
			}
			if !reflect.DeepEqual(ecl.bytecode, acl.bytecode) {
				return false
			}
			if !reflect.DeepEqual(ecl.vars, acl.vars) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(exp, act)
}

// test helper: construct a userDefined pointer and set its clauses
func udWithClauses(u *userDefined, cs []*clause) *userDefined {
	// Ensure clauses are fully compiled and initialized before publishing.
	// Tests sometimes construct *clause values with only `raw` set; compile
	// them here so the production code can rely on compiled fields like
	// `rulified`, `bytecode`, and `fingerprint` instead of using fallbacks.
	var compiled clauses
	for _, c := range cs {
		if c == nil {
			continue
		}
		if c.rulified != nil && len(c.bytecode) > 0 && c.fingerprint != 0 {
			compiled = append(compiled, c)
			continue
		}
		// compile may return multiple clauses (e.g., from alternation),
		// so merge the results. Use a fresh env per clause to avoid
		// accidental variable id collisions between clauses.
		cs2, err := compile(c.raw, NewEnv())
		if err != nil {
			// If compilation fails unexpectedly in a test helper, fall back
			// to using the original clause to avoid hiding test author
			// mistakes. The expectation is that tests supply valid clauses.
			compiled = append(compiled, c)
			continue
		}
		for _, cc := range cs2 {
			compiled = append(compiled, cc)
		}
	}
	u.setClauses(compiled)
	return u
}
