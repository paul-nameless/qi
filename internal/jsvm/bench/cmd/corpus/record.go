package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var seeds = map[string][3]string{
	"t3_test":   {"internal/pricing/tax.go", "subtotal >= freeOver", "subtotal > freeOver"},
	"t5_import": {"internal/server/handlers.go", `"shop/internal/cart"`, `"shop/internal/carts"`},
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func seed(task, dir string) error {
	sd, ok := seeds[task]
	if !ok {
		return nil
	}
	p := filepath.Join(dir, sd[0])
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), sd[1]) {
		return fmt.Errorf("seed %s: %q not found in %s", task, sd[1], p)
	}
	return os.WriteFile(p, []byte(strings.Replace(string(b), sd[1], sd[2], 1)), 0o644)
}

func taskOf(rid string) string {
	parts := strings.Split(rid, "_")
	if len(parts) < 5 {
		return ""
	}
	return parts[2] + "_" + parts[3]
}

func recordAll(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: corpus record SPIKE_TRANSCRIPTS_DIR SCRATCH_DIR [RID...]")
	}
	src, scratch := args[0], args[1]
	files, _ := filepath.Glob(filepath.Join(src, "*.json"))
	if len(args) > 2 {
		files = nil
		for _, r := range args[2:] {
			files = append(files, filepath.Join(src, r+".json"))
		}
	}
	bin, err := filepath.Abs("bin/goja")
	if err != nil {
		return err
	}
	for _, d := range []string{"home", "gocache", "gopath", "tmp", "work", "tapes"} {
		if err := os.MkdirAll(filepath.Join(scratch, d), 0o755); err != nil {
			return err
		}
	}
	for _, f := range files {
		rid := strings.TrimSuffix(filepath.Base(f), ".json")
		if err := recordOne(bin, f, rid, scratch); err != nil {
			return err
		}
	}
	return nil
}

func recordOne(bin, file, rid, scratch string) error {
	work := filepath.Join(scratch, "work", rid)
	if _, err := os.Stat(work); err == nil {
		if err := exec.Command("trash", work).Run(); err != nil {
			return fmt.Errorf("trash %s: %w", work, err)
		}
	}
	if err := copyTree("corpus/replay/fixture", work); err != nil {
		return err
	}
	if err := seed(taskOf(rid), work); err != nil {
		return err
	}
	out := filepath.Join(scratch, "tapes", rid+".json")
	profile, _ := filepath.Abs("corpus/replay/sb.profile")
	cmd := exec.Command("sandbox-exec", "-f", profile, bin, "record", file, work, out)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/opt/homebrew/bin:/usr/bin:/bin", "HOME=" + scratch + "/home", "GOCACHE=" + scratch + "/gocache", "TMPDIR=" + scratch + "/tmp", "GOPATH=" + scratch + "/gopath", "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=-mod=mod"}
	t := time.Now()
	b, err := cmd.CombinedOutput()
	fmt.Printf("%s %.1fs %v\n", rid, time.Since(t).Seconds(), err)
	if err != nil {
		return fmt.Errorf("record %s: %w\n%s", rid, err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join("corpus/replay/transcripts", rid+".json"), data, 0o644)
}
