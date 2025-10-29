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
	u.setClauses(clauses(cs))
	return u
}
