package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type userDefined struct {
	public        bool
	dynamic       bool
	multifile     bool
	discontiguous bool

	// 7.4.3 says "If no clauses are defined for a procedure indicated by a directive ... then the procedure shall exist but have no clauses."
	// clausesHolder holds an atomically-updatable pointer to the slice of
	// clause pointers for this predicate. Readers may take a snapshot via
	// getClauses() without holding the VM lock; writers should use
	// setClauses() under the existing vm.mu contract.
	clausesHolder

	// mu protects mutations that modify u.clauses in-place. This allows us to
	// perform compaction without holding the global vm.mu for the entire
	// operation, reducing global contention (lock order: vm.mu -> u.mu).
	mu sync.Mutex

	// deleted is a fast, atomic counter of tombstoned clauses. Retract will
	// increment this when it successfully claims a clause and compaction will
	// reset it to zero after reclaiming tombstones.
	deleted int32
}

type clauses []*clause

// clausesHolder wraps an atomic pointer to a clauses value so callers can
// atomically load/store the slice reference. We allocate a new value when
// storing to avoid races with readers.
type clausesHolder struct {
	p atomic.Pointer[clauses]
}

func (h *clausesHolder) load() clauses {
	pp := h.p.Load()
	if pp == nil {
		return nil
	}
	return *pp
}

func (h *clausesHolder) store(cs clauses) {
	np := new(clauses)
	*np = cs
	h.p.Store(np)
}

// Helper accessors on userDefined are provided below.

func (cs clauses) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	var p *Promise
	ks := make([]func(context.Context) *Promise, 0, len(cs))
	for i := range cs {
		c := cs[i]
		// skip clauses marked deleted
		if atomic.LoadUint32(&c.deleted) != 0 {
			continue
		}
		ks = append(ks, func(context.Context) *Promise {
			vars := make([]Variable, len(c.vars))
			for i := range vars {
				vars[i] = NewVariable()
			}
			return vm.exec(c.bytecode, vars, k, args, nil, env, p)
		})
	}
	p = Delay(ks...)
	return p
}

// getClauses returns a snapshot of the clause slice.
func (u *userDefined) getClauses() clauses {
	return u.clausesHolder.load()
}

// setClauses sets the clause slice atomically. Callers should hold vm.mu
// where appropriate when performing a read-modify-write.
func (u *userDefined) setClauses(cs clauses) {
	u.clausesHolder.store(cs)
}

// call dispatches to the current clause list. This allows userDefined to
// implement the procedure interface by delegating to the active clauses
// snapshot.
func (u *userDefined) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	cs := u.getClauses()
	return cs.call(vm, args, k, env)
}

func compile(t Term, env *Env) (clauses, error) {
	t = env.Resolve(t)
	if t, ok := t.(Compound); ok && t.Functor() == atomIf && t.Arity() == 2 {
		var cs clauses
		head, body := t.Arg(0), t.Arg(1)
		iter := altIterator{Alt: body, Env: env}
		for iter.Next() {
			c, err := compileClause(head, iter.Current(), env)
			if err != nil {
				return nil, typeError(validTypeCallable, body, env)
			}
			c.raw = t
			// cache the rulified form at compile time to avoid allocating a
			// new wrapper in Retract for every clause scan.
			c.rulified = rulify(c.raw, env)
			// compute a small fingerprint of the compiled bytecode for fast-path comparisons
			c.fingerprint = bytecodeFingerprint(c.bytecode)
			// store as pointer
			cs = append(cs, &c)
		}
		return cs, nil
	}

	c, err := compileClause(t, nil, env)
	c.raw = env.simplify(t)
	c.rulified = rulify(c.raw, env)
	c.fingerprint = bytecodeFingerprint(c.bytecode)
	return []*clause{&c}, err
}

func bytecodeFingerprint(b bytecode) uint64 {
	const prime uint64 = 1099511628211
	var h uint64 = 14695981039346656037
	for _, ins := range b {
		h ^= uint64(ins.opcode)
		h *= prime
		switch v := ins.operand.(type) {
		case procedureIndicator:
			// mix name bytes
			s := v.name.String()
			for i := 0; i < len(s); i++ {
				h ^= uint64(s[i])
				h *= prime
			}
			h ^= uint64(int64(v.arity))
			h *= prime
		case Integer:
			h ^= uint64(int64(v))
			h *= prime
		case Atom:
			s := v.String()
			for i := 0; i < len(s); i++ {
				h ^= uint64(s[i])
				h *= prime
			}
		default:
			// fallback: mix the type name to reduce collisions across operand types
			s := fmt.Sprintf("%T", v)
			for i := 0; i < len(s); i++ {
				h ^= uint64(s[i])
				h *= prime
			}
		}
	}
	return h
}

type clause struct {
	pi  procedureIndicator
	raw Term
	// rulified is the canonical rule form of raw, i.e. ensures it's an if/2
	// term (H:-B). Precomputing this at compile time avoids repeated
	// allocations in hot paths like Retract where we only need the normalized
	// form for matching.
	rulified Term
	vars     []Variable
	bytecode bytecode
	// fingerprint is a small, compile-time hash of bytecode used for
	// fast-rejection in equality checks (cheap and low-collision). It is
	// computed during compilation to avoid repeated work at runtime.
	fingerprint uint64
	// deleted is a marker (0 == active, 1 == deleted) set atomically by Retract/Abolish.
	deleted uint32
}

func compileClause(head Term, body Term, env *Env) (clause, error) {
	var c clause
	c.compileHead(head, env)
	if body != nil {
		if err := c.compileBody(body, env); err != nil {
			return c, typeError(validTypeCallable, body, env)
		}
	}
	c.bytecode = append(c.bytecode, instruction{opcode: opExit})
	return c, nil
}

func (c *clause) compileHead(head Term, env *Env) {
	switch head := env.Resolve(head).(type) {
	case Atom:
		c.pi = procedureIndicator{name: head, arity: 0}
	case Compound:
		c.pi = procedureIndicator{name: head.Functor(), arity: Integer(head.Arity())}
		for i := 0; i < head.Arity(); i++ {
			c.compileHeadArg(head.Arg(i), env)
		}
	}
}

func (c *clause) compileBody(body Term, env *Env) error {
	c.bytecode = append(c.bytecode, instruction{opcode: opEnter})
	iter := seqIterator{Seq: body, Env: env}
	for iter.Next() {
		if err := c.compilePred(iter.Current(), env); err != nil {
			return err
		}
	}
	return nil
}

var errNotCallable = errors.New("not callable")

func (c *clause) compilePred(p Term, env *Env) error {
	switch p := env.Resolve(p).(type) {
	case Variable:
		return c.compilePred(atomCall.Apply(p), env)
	case Atom:
		switch p {
		case atomCut:
			c.bytecode = append(c.bytecode, instruction{opcode: opCut})
			return nil
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opCall, operand: procedureIndicator{name: p, arity: 0}})
		return nil
	case Compound:
		for i := 0; i < p.Arity(); i++ {
			c.compileBodyArg(p.Arg(i), env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opCall, operand: procedureIndicator{name: p.Functor(), arity: Integer(p.Arity())}})
		return nil
	default:
		return errNotCallable
	}
}

func (c *clause) compileHeadArg(a Term, env *Env) {
	switch a := env.Resolve(a).(type) {
	case Variable:
		c.bytecode = append(c.bytecode, instruction{opcode: opGetVar, operand: c.varOffset(a)})
	case charList, codeList: // Treat them as if they're atomic.
		c.bytecode = append(c.bytecode, instruction{opcode: opGetConst, operand: a})
	case list:
		c.bytecode = append(c.bytecode, instruction{opcode: opGetList, operand: Integer(len(a))})
		for _, arg := range a {
			c.compileHeadArg(arg, env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	case *partial:
		prefix := a.Compound.(list)
		c.bytecode = append(c.bytecode, instruction{opcode: opGetPartial, operand: Integer(len(prefix))})
		c.compileHeadArg(*a.tail, env)
		for _, arg := range prefix {
			c.compileHeadArg(arg, env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	case Compound:
		c.bytecode = append(c.bytecode, instruction{opcode: opGetFunctor, operand: procedureIndicator{name: a.Functor(), arity: Integer(a.Arity())}})
		for i := 0; i < a.Arity(); i++ {
			c.compileHeadArg(a.Arg(i), env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	default:
		c.bytecode = append(c.bytecode, instruction{opcode: opGetConst, operand: a})
	}
}

func (c *clause) compileBodyArg(a Term, env *Env) {
	switch a := env.Resolve(a).(type) {
	case Variable:
		c.bytecode = append(c.bytecode, instruction{opcode: opPutVar, operand: c.varOffset(a)})
	case charList, codeList: // Treat them as if they're atomic.
		c.bytecode = append(c.bytecode, instruction{opcode: opPutConst, operand: a})
	case list:
		c.bytecode = append(c.bytecode, instruction{opcode: opPutList, operand: Integer(len(a))})
		for _, arg := range a {
			c.compileBodyArg(arg, env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	case *partial:
		var l int
		iter := ListIterator{List: a.Compound}
		for iter.Next() {
			l++
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPutPartial, operand: Integer(l)})
		c.compileBodyArg(*a.tail, env)
		iter = ListIterator{List: a.Compound}
		for iter.Next() {
			c.compileBodyArg(iter.Current(), env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	case Compound:
		c.bytecode = append(c.bytecode, instruction{opcode: opPutFunctor, operand: procedureIndicator{name: a.Functor(), arity: Integer(a.Arity())}})
		for i := 0; i < a.Arity(); i++ {
			c.compileBodyArg(a.Arg(i), env)
		}
		c.bytecode = append(c.bytecode, instruction{opcode: opPop})
	default:
		c.bytecode = append(c.bytecode, instruction{opcode: opPutConst, operand: a})
	}
}

func (c *clause) varOffset(o Variable) Integer {
	for i, v := range c.vars {
		if v == o {
			return Integer(i)
		}
	}
	c.vars = append(c.vars, o)
	return Integer(len(c.vars) - 1)
}
