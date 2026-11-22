// Package jsvm is a small JavaScript engine for qi's code mode.
//
// Source is parsed into an AST, every local is resolved to a frame slot or a
// heap cell, and the program is compiled into a tree of Go closures. Values are
// a 24-byte struct (a float64 plus an interface for everything that is not a
// number), so arithmetic never allocates. Objects use shared hidden-class
// shapes with per-site inline caches; arrays are dense slices.
//
// # Cells
//
// RunCell runs a cell: a script body where top-level await and top-level
// return are allowed. Top-level var, let, const and function declarations
// become persistent globals that later cells may redeclare, even const. The
// result is the return value, else the value of the cell's final statement
// when it is an expression statement. A cell that throws keeps the globals it
// assigned before the throw. A cell containing top-level await returns a
// promise; the embedder drives it with RunJobs and PromiseResult.
//
// # Embedding
//
// A VM is used from one goroutine at a time. Host functions registered with
// SetFunc run synchronously; asynchronous host work returns a promise from
// NewPromise and settles it later from the embedder's goroutine, followed by
// RunJobs. Call RunJobs after every RunCell, including one that returned an
// error: promise jobs a cell queued before it threw stay queued (as microtasks
// do in a REPL) and run on the next RunJobs; AbandonPending drops them instead.
// Interrupt may be called from any goroutine: the running cell fails with
// ErrInterrupted, which JavaScript cannot catch, also when it lands inside a
// host function's nested Call, CallValue or JSONStringify, and the VM stays
// usable after ClearInterrupt. An interrupted RunCell or RunJobs also unwinds
// the async functions of the most recent cell that are still suspended in
// await, so they never resume; queued jobs stay queued. Uncaught JavaScript
// errors are returned as *Exception with name, message, a V8-style stack ("    at fn (cell.js:LINE:COL)") and
// the line and column of the innermost frame; syntax errors carry the line and
// column of the offending token. Async functions run on coroutines
// (iter.Pull), each with its own value stack; AbandonPending and Close unwind
// the ones still suspended and drop queued jobs. The zero Value is the number
// 0, not undefined: host functions should return Undefined() explicitly.
//
// # Limits
//
// Options.MaxDepth bounds the JavaScript call depth, including recursion
// inside builtins such as join and bound functions (RangeError, catchable).
// Options.MemoryLimit is checked against the Go heap (objects plus goroutine
// stacks) every 16 MB of tracked allocation; exceeding it after a forced GC
// throws a catchable RangeError. Strings are capped at 2^29-24 bytes and at
// most 100000 async activations may be pending at once (RangeError).
// Source nesting (expressions, statements, patterns, chains such as a+b+...
// or a.b.c...) is capped at 400 levels with a SyntaxError, so no input can
// exhaust the Go stack; recursion inside builtins counts toward MaxDepth.
// Allocations sized by script input are checked against the limit before
// they happen. A Go runtime panic inside a cell (an engine bug) becomes a
// catchable InternalError and the VM stays usable.
// Interrupts are checked on loop back-edges and calls. Strings passed in
// from the host (Str, JSONParse, cell source, host errors) are sanitised:
// invalid UTF-8 becomes U+FFFD; valid input is not copied.
//
// # Language
//
// Everything runs as strict mode, except that assigning to an undeclared name
// creates a global. Top-level this is globalThis, and cell-level bindings are
// globalThis properties: globalThis.x = v updates a cell's const x. Supported: var/let/const, functions, closures, arrows,
// default and rest parameters, this, new on functions, object and array
// literals with spread, shorthand and computed keys, destructuring everywhere,
// template literals and String.raw, all operators including ?., ??, ** and
// logical assignment, if/for/for-in/for-of/while/do-while/switch,
// break/continue, try/catch/finally, async functions and await. Iteration
// covers arrays, strings, Map, Set and their keys/values/entries iterators;
// there is no user-defined iterator protocol.
//
// Unsupported syntax fails at compile time with "SyntaxError: unsupported:
// <feature>" and a line number: classes, generators, getters and setters,
// labels, tagged templates other than String.raw, for await, arguments,
// BigInt, modules and dynamic import, with, eval, using declarations.
// Missing builtins (Symbol, WeakMap, Proxy, Reflect, Intl, typed arrays)
// are ReferenceErrors. Date is complete except toLocaleString and
// toLocaleTimeString, which throw; local time comes from time.Local and
// toLocaleDateString prints MM/DD/YYYY. let and const have a temporal dead
// zone; the resolver adds the check only to references it cannot prove
// initialised. A regular expression that exceeds its step budget or fails inside
// the regex package throws a catchable Error.
//
// Strings are Go strings with UTF-16 semantics for length and indexing (ASCII
// strings index by byte, others through a cached UTF-16 view); lone
// surrogates, including half of a pair split by indexing or a non-u regex
// match, become U+FFFD. Concatenation builds ropes, so appending in a loop
// is linear. Arrays support holes (Array(n), elisions, delete, length
// growth) with the spec's per-method rules. String.prototype.normalize only
// accepts ASCII input and
// localeCompare approximates ICU root collation (punctuation < symbols <
// digits < letters, accents and case as secondary and tertiary differences,
// with the numeric option); locale arguments are ignored. Number and Date
// toLocale* methods accept only en-US and throw for other locales and options.
package jsvm
