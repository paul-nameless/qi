package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCellRequiredArgs(t *testing.T) {
	t.Parallel()
	k := newCellKit(t, Subagent("m", nil))
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) { return "ok", nil }
	k.file("out.txt", "keep\n")
	res := k.wantErr(`const obj = {}; await write("out.txt", obj.typo)`, "ToolError: write: content is undefined\n    at cell.js:1:")
	if len(res.Calls) != 0 {
		t.Errorf("a call with a missing argument ran: %+v", res.Calls)
	}
	k.wantErr(`await edit("out.txt", "keep", undefined)`, "edit: new (new_string) is undefined")
	k.wantErr(`await edit("out.txt", "keep", null)`, "edit: new (new_string) is null")
	k.wantErr(`await write({file_path: "out.txt"})`, "write: content is undefined")
	k.wantErr(`await read()`, "read: path (file_path) is undefined")
	k.wantErr(`await edit("out.txt", "keep", "x", {all: true, replace_all: false})`, "edit: both all and replace_all given")
	if data, _ := os.ReadFile(filepath.Join(k.dir, "out.txt")); string(data) != "keep\n" {
		t.Errorf("out.txt changed: %q", data)
	}
	k.want(`await write("empty.txt", ""); JSON.stringify(await read("empty.txt"))`, `""`)
	k.want(`await subagent("quick")`, "ok")
}
