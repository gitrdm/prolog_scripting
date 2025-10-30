// Locking and concurrency guidance for this package
//
// Lock ordering rules (important):
//  - vm.mu (RWMutex)  -> vm.streams.mu (RWMutex) -> s.mu (Mutex)
//    Always acquire parent locks before child locks to avoid inversion.
//  - Prefer read locks (RLock) for lookups (e.g. Arrive/procedure lookup). Use write locks
//    (Lock) for mutating operations (Register, assert/retract, SetPrologFlag, installing
//    compiled clauses, modifying vm.loaded, etc.).
//  - Do not hold vm.mu while performing long-running work (compilation, I/O). Instead
//    prepare data off-lock and briefly Lock() to install the result.
//  - The interpreter hot loop (exec) must not acquire vm.mu; snapshot read-only VM tables
//    (operators, procedure pointers) before entering the loop if needed.
//
// Notes:
//  - streams.add/remove/lookup use vm.streams' locks; individual Stream objects use s.mu
//    to protect their internal buf/position/endOfStream state.
//  - SetUserInput/SetUserOutput must acquire vm.mu and use vm.streams in the correct order
//    (vm.mu then vm.streams.mu) to avoid races.
//
// This comment serves as guidance for contributors and reviewers when adding new
// synchronization or modifying existing code paths.

package engine

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"sync"
)

type bytecode []instruction

type instruction struct {
	opcode  opcode
	operand Term
}

type opcode byte

const (
	opEnter opcode = iota
	opCall
	opExit
	opGetConst
	opPutConst
	opGetVar
	opPutVar
	opGetFunctor
	opPutFunctor
	opPop

	opCut
	opGetList
	opPutList
	opGetPartial
	opPutPartial
)

// Success is a continuation that leads to true.
func Success(*Env) *Promise {
	return Bool(true)
}

// Failure is a continuation that leads to false.
func Failure(*Env) *Promise {
	return Bool(false)
}

// VM is the core of a Prolog interpreter. The zero value for VM is a valid VM without any builtin predicates.
type VM struct {
	// Unknown is a callback that is triggered when the VM reaches to an unknown predicate while current_prolog_flag(unknown, warning).
	Unknown func(name Atom, args []Term, env *Env)

	procedures map[procedureIndicator]procedure
	unknown    unknownAction

	// FS is a file system that is referenced when the VM loads Prolog texts e.g. ensure_loaded/1.
	// It has no effect on open/4 nor open/3 which always access the actual file system.
	FS     fs.FS
	loaded map[string]struct{}

	mu sync.RWMutex

	// Internal/external expression
	operators       operators
	charConversions map[rune]rune
	charConvEnabled bool
	doubleQuotes    doubleQuotes

	// I/O
	streams       streams
	input, output *Stream

	// Misc
	debug bool
}

// Register0 registers a predicate of arity 0.
func (vm *VM) Register0(name Atom, p Predicate0) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 0}] = p
}

// Register1 registers a predicate of arity 1.
func (vm *VM) Register1(name Atom, p Predicate1) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 1}] = p
}

// Register2 registers a predicate of arity 2.
func (vm *VM) Register2(name Atom, p Predicate2) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 2}] = p
}

// Register3 registers a predicate of arity 3.
func (vm *VM) Register3(name Atom, p Predicate3) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 3}] = p
}

// Register4 registers a predicate of arity 4.
func (vm *VM) Register4(name Atom, p Predicate4) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 4}] = p
}

// Register5 registers a predicate of arity 5.
func (vm *VM) Register5(name Atom, p Predicate5) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 5}] = p
}

// Register6 registers a predicate of arity 6.
func (vm *VM) Register6(name Atom, p Predicate6) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 6}] = p
}

// Register7 registers a predicate of arity 7.
func (vm *VM) Register7(name Atom, p Predicate7) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 7}] = p
}

// Register8 registers a predicate of arity 8.
func (vm *VM) Register8(name Atom, p Predicate8) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[procedureIndicator{name: name, arity: 8}] = p
}

type unknownAction int

const (
	unknownError unknownAction = iota
	unknownFail
	unknownWarning
)

func (u unknownAction) String() string {
	return [...]string{
		unknownError:   "error",
		unknownFail:    "fail",
		unknownWarning: "warning",
	}[u]
}

type procedure interface {
	call(*VM, []Term, Cont, *Env) *Promise
}

// Cont is a continuation.
type Cont func(*Env) *Promise

// Arrive is the entry point of the VM.
func (vm *VM) Arrive(name Atom, args []Term, k Cont, env *Env) (promise *Promise) {
	defer ensurePromise(&promise)

	if vm.Unknown == nil {
		vm.Unknown = func(Atom, []Term, *Env) {}
	}

	pi := procedureIndicator{name: name, arity: Integer(len(args))}
	vm.mu.RLock()
	p, ok := vm.procedures[pi]
	unknown := vm.unknown
	vm.mu.RUnlock()
	if !ok {
		switch unknown {
		case unknownWarning:
			vm.Unknown(name, args, env)
			fallthrough
		case unknownFail:
			return Bool(false)
		default:
			return Error(existenceError(objectTypeProcedure, pi.Term(), env))
		}
	}

	// bind the special variable to inform the predicate about the context.
	env = env.bind(varContext, pi.Term())

	return p.call(vm, args, k, env)
}

func (vm *VM) exec(pc bytecode, vars []Variable, cont Cont, args []Term, astack [][]Term, env *Env, cutParent *Promise) *Promise {
	var (
		ok  = true
		op  instruction
		arg Term
	)
	for ok {
		op, pc = pc[0], pc[1:]
		switch opcode, operand := op.opcode, op.operand; opcode {
		case opGetConst:
			arg, args = args[0], args[1:]
			env, ok = env.Unify(arg, operand)
		case opPutConst:
			args = append(args, operand)
		case opGetVar:
			v := vars[operand.(Integer)]
			arg, args = args[0], args[1:]
			env, ok = env.Unify(arg, v)
		case opPutVar:
			v := vars[operand.(Integer)]
			args = append(args, v)
		case opGetFunctor:
			pi := operand.(procedureIndicator)
			arg, astack = env.Resolve(args[0]), append(astack, args[1:])
			args = make([]Term, int(pi.arity))
			for i := range args {
				args[i] = NewVariable()
			}
			env, ok = env.Unify(arg, pi.name.Apply(args...))
		case opPutFunctor:
			pi := operand.(procedureIndicator)
			vs := make([]Term, int(pi.arity))
			arg = pi.name.Apply(vs...)
			args = append(args, arg)
			astack = append(astack, args)
			args = vs[:0]
		case opPop:
			args, astack = astack[len(astack)-1], astack[:len(astack)-1]
		case opEnter:
			break
		case opCall:
			pi := operand.(procedureIndicator)
			return vm.Arrive(pi.name, args, func(env *Env) *Promise {
				return vm.exec(pc, vars, cont, nil, nil, env, cutParent)
			}, env)
		case opExit:
			return cont(env)
		case opCut:
			return cut(cutParent, func(context.Context) *Promise {
				return vm.exec(pc, vars, cont, args, astack, env, cutParent)
			})
		case opGetList:
			l := operand.(Integer)
			arg, astack = args[0], append(astack, args[1:])
			args = make([]Term, int(l))
			for i := range args {
				args[i] = NewVariable()
			}
			env, ok = env.Unify(arg, list(args))
		case opPutList:
			l := operand.(Integer)
			vs := make([]Term, int(l))
			arg = list(vs)
			args = append(args, arg)
			astack = append(astack, args)
			args = vs[:0]
		case opGetPartial:
			l := operand.(Integer)
			arg, astack = args[0], append(astack, args[1:])
			args = make([]Term, int(l+1))
			for i := range args {
				args[i] = NewVariable()
			}
			env, ok = env.Unify(arg, PartialList(args[0], args[1:]...))
		case opPutPartial:
			l := operand.(Integer)
			vs := make([]Term, int(l+1))
			arg = &partial{
				Compound: list(vs[1:]),
				tail:     &vs[0],
			}
			args = append(args, arg)
			astack = append(astack, args)
			args = vs[:0]
		}
	}

	return Bool(false)
}

// SetUserInput sets the given stream as user_input.
func (vm *VM) SetUserInput(s *Stream) {
	s.vm = vm
	s.alias = atomUserInput
	vm.mu.Lock()
	vm.streams.add(s)
	vm.input = s
	vm.mu.Unlock()
}

// SetUserOutput sets the given stream as user_output.
func (vm *VM) SetUserOutput(s *Stream) {
	s.vm = vm
	s.alias = atomUserOutput
	vm.mu.Lock()
	vm.streams.add(s)
	vm.output = s
	vm.mu.Unlock()
}

// Predicate0 is a predicate of arity 0.
type Predicate0 func(*VM, Cont, *Env) *Promise

func (p Predicate0) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 0 {
		return Error(&wrongNumberOfArgumentsError{expected: 0, actual: args})
	}

	return p(vm, k, env)
}

// Predicate1 is a predicate of arity 1.
type Predicate1 func(*VM, Term, Cont, *Env) *Promise

func (p Predicate1) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 1 {
		return Error(&wrongNumberOfArgumentsError{expected: 1, actual: args})
	}

	return p(vm, args[0], k, env)
}

// Predicate2 is a predicate of arity 2.
type Predicate2 func(*VM, Term, Term, Cont, *Env) *Promise

func (p Predicate2) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 2 {
		return Error(&wrongNumberOfArgumentsError{expected: 2, actual: args})
	}

	return p(vm, args[0], args[1], k, env)
}

// Predicate3 is a predicate of arity 3.
type Predicate3 func(*VM, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate3) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 3 {
		return Error(&wrongNumberOfArgumentsError{expected: 3, actual: args})
	}

	return p(vm, args[0], args[1], args[2], k, env)
}

// Predicate4 is a predicate of arity 4.
type Predicate4 func(*VM, Term, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate4) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 4 {
		return Error(&wrongNumberOfArgumentsError{expected: 4, actual: args})
	}

	return p(vm, args[0], args[1], args[2], args[3], k, env)
}

// Predicate5 is a predicate of arity 5.
type Predicate5 func(*VM, Term, Term, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate5) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 5 {
		return Error(&wrongNumberOfArgumentsError{expected: 5, actual: args})
	}

	return p(vm, args[0], args[1], args[2], args[3], args[4], k, env)
}

// Predicate6 is a predicate of arity 6.
type Predicate6 func(*VM, Term, Term, Term, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate6) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 6 {
		return Error(&wrongNumberOfArgumentsError{expected: 6, actual: args})
	}

	return p(vm, args[0], args[1], args[2], args[3], args[4], args[5], k, env)
}

// Predicate7 is a predicate of arity 7.
type Predicate7 func(*VM, Term, Term, Term, Term, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate7) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 7 {
		return Error(&wrongNumberOfArgumentsError{expected: 7, actual: args})
	}

	return p(vm, args[0], args[1], args[2], args[3], args[4], args[5], args[6], k, env)
}

// Predicate8 is a predicate of arity 8.
type Predicate8 func(*VM, Term, Term, Term, Term, Term, Term, Term, Term, Cont, *Env) *Promise

func (p Predicate8) call(vm *VM, args []Term, k Cont, env *Env) *Promise {
	if len(args) != 8 {
		return Error(&wrongNumberOfArgumentsError{expected: 8, actual: args})
	}

	return p(vm, args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], k, env)
}

// procedureIndicator identifies a procedure e.g. (=)/2.
type procedureIndicator struct {
	name  Atom
	arity Integer
}

func (p procedureIndicator) WriteTerm(w io.Writer, opts *WriteOptions, env *Env) error {
	return WriteCompound(w, p, opts, env)
}

func (p procedureIndicator) Compare(t Term, env *Env) int {
	return CompareCompound(p, t, env)
}

func (p procedureIndicator) Functor() Atom {
	return atomSlash
}

func (p procedureIndicator) Arity() int {
	return 2
}

func (p procedureIndicator) Arg(n int) Term {
	if n == 0 {
		return p.name
	}
	return p.arity
}

func (p procedureIndicator) String() string {
	var sb strings.Builder
	_ = p.name.WriteTerm(&sb, &WriteOptions{
		quoted: true,
	}, nil)
	_, _ = fmt.Fprintf(&sb, "/%d", p.arity)
	return sb.String()
}

// Term returns p as term.
func (p procedureIndicator) Term() Term {
	return atomSlash.Apply(p.name, p.arity)
}

// Clone creates a fresh VM configuration copied from vm. The returned VM has
// copied predicate and operator configuration but does not share mutable
// runtime fields such as streams, loaded files, or input/output streams.
func (vm *VM) Clone() *VM {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	n := &VM{}
	n.Unknown = vm.Unknown
	n.FS = vm.FS
	n.unknown = vm.unknown

	// copy procedures
	if vm.procedures != nil {
		n.procedures = make(map[procedureIndicator]procedure, len(vm.procedures))
		for k, v := range vm.procedures {
			n.procedures[k] = v
		}
	}

	// copy operators
	if vm.operators != nil {
		n.operators = make(operators)
		for k, v := range vm.operators {
			n.operators[k] = v
		}
	}

	// copy char conversions
	if vm.charConversions != nil {
		n.charConversions = make(map[rune]rune, len(vm.charConversions))
		for k, v := range vm.charConversions {
			n.charConversions[k] = v
		}
	}
	n.charConvEnabled = vm.charConvEnabled
	n.doubleQuotes = vm.doubleQuotes

	// FS, Unknown, procedures, operators, and char conversions are copied.
	// Mutable runtime fields like streams, loaded, input/output are left empty
	// so the clone is safe to use concurrently as a per-request VM.

	return n
}

// LookupProcedure returns the procedure registered for pi. It acquires
// vm.mu.RLock() internally so callers don't need to hold the VM lock for
// simple lookups.
func (vm *VM) LookupProcedure(pi procedureIndicator) (procedure, bool) {
	vm.mu.RLock()
	p, ok := vm.procedures[pi]
	vm.mu.RUnlock()
	return p, ok
}

// InstallProcedure installs p under the given procedure indicator. The
// method acquires vm.mu.Lock() internally and ensures the map is allocated.
func (vm *VM) InstallProcedure(pi procedureIndicator, p procedure) {
	vm.mu.Lock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[pi] = p
	vm.mu.Unlock()
}

// RemoveProcedure deletes the procedure entry for pi. It acquires the write
// lock internally.
func (vm *VM) RemoveProcedure(pi procedureIndicator) {
	vm.mu.Lock()
	if vm.procedures != nil {
		delete(vm.procedures, pi)
	}
	vm.mu.Unlock()
}

// ProceduresCopy returns a shallow copy of the procedures map. The copy can
// be safely iterated without holding vm.mu.
func (vm *VM) ProceduresCopy() map[procedureIndicator]procedure {
	vm.mu.RLock()
	if vm.procedures == nil {
		vm.mu.RUnlock()
		return nil
	}
	cp := make(map[procedureIndicator]procedure, len(vm.procedures))
	for k, v := range vm.procedures {
		cp[k] = v
	}
	vm.mu.RUnlock()
	return cp
}

// Input returns the current input stream in a concurrency-safe way.
func (vm *VM) Input() *Stream {
	vm.mu.RLock()
	s := vm.input
	vm.mu.RUnlock()
	return s
}

// Output returns the current output stream in a concurrency-safe way.
func (vm *VM) Output() *Stream {
	vm.mu.RLock()
	s := vm.output
	vm.mu.RUnlock()
	return s
}

// OperatorsCopy returns a shallow copy of the operators table. The copy can
// be safely accessed without holding vm.mu.
func (vm *VM) OperatorsCopy() operators {
	vm.mu.RLock()
	if vm.operators == nil {
		vm.mu.RUnlock()
		return nil
	}
	cp := make(operators, len(vm.operators))
	for k, v := range vm.operators {
		cp[k] = v
	}
	vm.mu.RUnlock()
	return cp
}

// DoubleQuotes returns the current double-quotes setting in a concurrency-safe
// way.
func (vm *VM) DoubleQuotes() doubleQuotes {
	vm.mu.RLock()
	dq := vm.doubleQuotes
	vm.mu.RUnlock()
	return dq
}

// CharConversionsCopy returns a shallow copy of the VM's character conversion
// map. The copy may be iterated without holding vm.mu.
func (vm *VM) CharConversionsCopy() map[rune]rune {
	vm.mu.RLock()
	if vm.charConversions == nil {
		vm.mu.RUnlock()
		return nil
	}
	cp := make(map[rune]rune, len(vm.charConversions))
	for k, v := range vm.charConversions {
		cp[k] = v
	}
	vm.mu.RUnlock()
	return cp
}

// CharConvEnabled returns whether character conversion is enabled on the VM.
func (vm *VM) CharConvEnabled() bool {
	vm.mu.RLock()
	b := vm.charConvEnabled
	vm.mu.RUnlock()
	return b
}

// SetCharConversion registers or updates a character conversion in a
// concurrency-safe manner. The VM lock is acquired internally.
func (vm *VM) SetCharConversion(from, to rune) {
	vm.mu.Lock()
	if vm.charConversions == nil {
		vm.charConversions = map[rune]rune{}
	}
	vm.charConversions[from] = to
	vm.mu.Unlock()
}

// ClearCharConversion removes a character conversion if present. The VM
// lock is acquired internally.
func (vm *VM) ClearCharConversion(from rune) {
	vm.mu.Lock()
	if vm.charConversions != nil {
		delete(vm.charConversions, from)
	}
	vm.mu.Unlock()
}

// SetCharConvEnabled sets whether character conversions are enabled. It
// acquires the VM lock internally.
func (vm *VM) SetCharConvEnabled(b bool) {
	vm.mu.Lock()
	vm.charConvEnabled = b
	vm.mu.Unlock()
}

// SetDebug enables or disables VM debug mode.
func (vm *VM) SetDebug(b bool) {
	vm.mu.Lock()
	vm.debug = b
	vm.mu.Unlock()
}

// SetUnknown sets the unknownAction for the VM.
func (vm *VM) SetUnknown(u unknownAction) {
	vm.mu.Lock()
	vm.unknown = u
	vm.mu.Unlock()
}

// SetDoubleQuotes updates the VM's doubleQuotes handling.
func (vm *VM) SetDoubleQuotes(dq doubleQuotes) {
	vm.mu.Lock()
	vm.doubleQuotes = dq
	vm.mu.Unlock()
}

// MarkLoaded records that filename is being/has been loaded. It returns
// true if the filename was already present (i.e., already loading/loaded),
// or false if it was newly marked.
func (vm *VM) MarkLoaded(f string) bool {
	vm.mu.Lock()
	if vm.loaded == nil {
		vm.loaded = map[string]struct{}{}
	}
	if _, ok := vm.loaded[f]; ok {
		vm.mu.Unlock()
		return true
	}
	vm.loaded[f] = struct{}{}
	vm.mu.Unlock()
	return false
}

// UnmarkLoaded removes the loading/loaded mark for filename.
func (vm *VM) UnmarkLoaded(f string) {
	vm.mu.Lock()
	if vm.loaded != nil {
		delete(vm.loaded, f)
	}
	vm.mu.Unlock()
}

// IsLoaded reports whether filename is present in the loaded set.
func (vm *VM) IsLoaded(f string) bool {
	vm.mu.RLock()
	_, ok := vm.loaded[f]
	vm.mu.RUnlock()
	return ok
}

// flagsSnapshot bundles several VM runtime flags read under a single lock.
type flagsSnapshot struct {
	charConvEnabled bool
	debug           bool
	unknown         unknownAction
	doubleQuotes    doubleQuotes
}

// FlagsSnapshot returns a consistent snapshot of commonly-read VM flags.
func (vm *VM) FlagsSnapshot() flagsSnapshot {
	vm.mu.RLock()
	fs := flagsSnapshot{
		charConvEnabled: vm.charConvEnabled,
		debug:           vm.debug,
		unknown:         vm.unknown,
		doubleQuotes:    vm.doubleQuotes,
	}
	vm.mu.RUnlock()
	return fs
}

// bytecodeEqual compares two bytecode sequences for equality. We use a deep
// comparison of the operand to keep the check simple and robust across
// different operand types (Term, procedureIndicator, Integer, etc.).
func bytecodeEqual(a, b bytecode) bool {
	if len(a) != len(b) {
		return false
	}
	// Fast-path: if the slices share the same backing array (common when we
	// took a direct reference instead of copying), the bytecode can't have
	// changed and we can return quickly.
	if len(a) > 0 && len(b) > 0 {
		if &a[0] == &b[0] {
			return true
		}
	}

	for i := range a {
		if a[i].opcode != b[i].opcode {
			return false
		}
		// Cheap identity check first. In the common case operands are the
		// same interface value (pointer equality) and this avoids the cost of
		// reflect.DeepEqual.
		if a[i].operand == b[i].operand {
			continue
		}
		// Fallback to deep-equality only when necessary to preserve the
		// original semantics.
		if !reflect.DeepEqual(a[i].operand, b[i].operand) {
			return false
		}
	}
	return true
}

// Apply applies p to args.
func (p procedureIndicator) Apply(args ...Term) (Term, error) {
	if p.arity != Integer(len(args)) {
		return nil, &wrongNumberOfArgumentsError{expected: int(p.arity), actual: args}
	}
	return p.name.Apply(args...), nil
}

func piArg(t Term, env *Env) (procedureIndicator, func(int) Term, error) {
	switch f := env.Resolve(t).(type) {
	case Variable:
		return procedureIndicator{}, nil, InstantiationError(env)
	case Atom:
		return procedureIndicator{name: f, arity: 0}, nil, nil
	case Compound:
		return procedureIndicator{name: f.Functor(), arity: Integer(f.Arity())}, f.Arg, nil
	default:
		return procedureIndicator{}, nil, typeError(validTypeCallable, f, env)
	}
}

type wrongNumberOfArgumentsError struct {
	expected int
	actual   []Term
}

func (e *wrongNumberOfArgumentsError) Error() string {
	return fmt.Sprintf("wrong number of arguments: expected=%d, actual=%s", e.expected, e.actual)
}
