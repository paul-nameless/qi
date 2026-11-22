package tools

import "strings"

const runIntro = `Run JavaScript in a persistent VM. This is your only tool: reading, searching, editing, running commands, MCP and subagents all happen through the async functions below. Variables and functions stay defined between calls, so keep large results in variables and print only what you need. Top-level await and return work. The result is everything console.log prints plus the value of the last expression or of return (a string as it is, other values as JSON), cut to head and tail past 2000 lines or 60 KB with the full text saved to a file.

Functions. All are async: call them with await. Each call goes through the user's hooks and permission checks and shows in their terminal. A failure throws ToolError (message; denied is true when a hook or read-only mode refused the call). Paths are relative to the working directory.
`

const runRules = `

Rules:
- A single call is fine: ` + "`return await read(\"go.mod\")`" + `. Start independent calls together with Promise.all (up to 8 run at once) and await every call you start.
- Promise.all rejects on the first failure and the results of the other calls are lost: for subagents and other slow calls use Promise.allSettled or catch each call.
- Results inside the VM are not cut, except bash output past its first and last 512 KB (truncated is then true; redirect to a file for more): filter, count and summarize in code instead of printing everything.
- Never run two edits of the same file at once.
- The names {names} are set again at the start of every run call: don't use them for your own variables.
- The call times out after 300 s (timeout changes it, up to 1800). bash() times out on its own after its timeout and returns the output so far with timed_out true, so a hanging command does not stop your code. Start long builds and test runs with {background: true}.
- A timeout or interrupt stops the code; variables assigned before it stay.
- The language is a JavaScript subset: no classes, generators, getters or setters, Symbol, labels, BigInt, import or require. There is no fs, process or fetch: use the functions above. sleep(ms), setTimeout and clearTimeout work.

Examples
Read a file:
  return await read("internal/app/run.go")
Run independent commands at once:
  const [vet, test] = await Promise.all([bash("go vet ./..."), bash("go test ./...")])
  console.log(vet.output); console.log(test.exit_code, test.output.slice(-3000))
The same change in many files:
  const files = (await bash("rg -l 'FormatMoney\\(' --glob '*.go'")).output.split("\n").filter(Boolean)
  for (const f of files) await edit(f, "FormatMoney(", "FormatPrice(", {all: true})
  return (await bash("go build ./... && go test ./...")).output
Handle a failure:
  try { await edit("a.go", "old text", "new text") } catch (e) { console.log("edit failed:", e.message) }`

func runDescription(inner *Registry) string {
	var docs []string
	for _, t := range inner.tools {
		if t.Code != nil && t.Code.Doc != "" {
			docs = append(docs, t.Code.Doc)
		}
	}
	return runIntro + strings.Join(docs, "\n") + strings.Replace(runRules, "{names}", reservedNames(inner), 1)
}

func reservedNames(inner *Registry) string {
	var names []string
	for _, t := range inner.tools {
		if t.Code != nil {
			names = append(names, t.Code.Name)
		}
	}
	names = append(names, "sleep", "setTimeout", "clearTimeout", "print")
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
