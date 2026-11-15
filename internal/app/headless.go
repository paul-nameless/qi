package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/ui"
)

func headlessPrompt(query string, stdinPiped bool) (string, error) {
	prompt := query
	if stdinPiped {
		data := readStdin(query == "")
		if s := strings.TrimSpace(data); s != "" {
			if prompt == "" {
				prompt = s
			} else {
				prompt += "\n\n<stdin>\n" + s + "\n</stdin>"
			}
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("empty prompt")
	}
	return prompt, nil
}

func stdinHasData() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeNamedPipe != 0 || st.Mode().IsRegular()
}

func readStdin(wait bool) string {
	first := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64<<10)
		n, _ := os.Stdin.Read(buf)
		first <- buf[:n]
	}()
	var head []byte
	if wait {
		head = <-first
	} else {
		select {
		case head = <-first:
		case <-time.After(5 * time.Second):
			fmt.Fprintln(os.Stderr, "qi: no stdin within 5s, running without it")
			return ""
		}
	}
	if len(head) == 0 {
		return ""
	}
	rest, _ := io.ReadAll(os.Stdin)
	return string(head) + string(rest)
}

func runHeadless(a *agent.Agent, display *ui.Printer, hooks *agent.Hooks, prompt string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		cancel()
	}()
	blocks, err := submitPrompt(ctx, a, display, hooks, prompt)
	if err != nil {
		return err
	}
	final, stats, err := a.Turn(ctx, blocks)
	display.EndBlock()
	display.Info(stats.String(a.ContextShort()) + " · " + resourceUsage())
	if err != nil {
		return err
	}
	fmt.Println(final)
	return nil
}
