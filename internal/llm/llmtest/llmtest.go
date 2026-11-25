package llmtest

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/paul-nameless/qi/internal/llm"
)

type Reply func(ctx context.Context, n int, req llm.Request) (llm.Message, error)

type Provider struct {
	Reply  Reply
	Usage  llm.Usage
	Echo   bool
	OnCall func(n int)

	mu       sync.Mutex
	requests []llm.Request
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	p.mu.Lock()
	n := len(p.requests)
	req.Messages = slices.Clone(req.Messages)
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.OnCall != nil {
		p.OnCall(n)
	}
	msg, err := p.reply(ctx, n, req)
	if err == nil && p.Echo {
		onDelta(llm.Delta{Kind: llm.DeltaText, Text: msg.Text()})
	}
	return msg, p.Usage, err
}

func (p *Provider) reply(ctx context.Context, n int, req llm.Request) (llm.Message, error) {
	if p.Reply == nil {
		return Script()(ctx, n, req)
	}
	return p.Reply(ctx, n, req)
}

func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

func (p *Provider) Request(n int) llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[n]
}

func (p *Provider) Last() llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[len(p.requests)-1]
}

func Script(replies ...llm.Message) Reply {
	return func(ctx context.Context, n int, _ llm.Request) (llm.Message, error) {
		if err := ctx.Err(); err != nil {
			return llm.Message{}, err
		}
		if n < len(replies) {
			return replies[n], nil
		}
		return Text("done"), nil
	}
}

func Steps(steps ...func(req llm.Request) (llm.Message, error)) Reply {
	return func(_ context.Context, n int, req llm.Request) (llm.Message, error) {
		if n < len(steps) {
			return steps[n](req)
		}
		return Text("done"), nil
	}
}

func Fail(err error) Reply {
	return func(context.Context, int, llm.Request) (llm.Message, error) {
		return llm.Message{}, err
	}
}

func Text(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func Call(id, name string, input any) llm.Message {
	in, _ := json.Marshal(input)
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: name, Input: in}}}
}
