package llm

import (
	"errors"
	"strings"
	"testing"
)

func feed(t *testing.T, st streamState, events ...string) (Message, Usage, error) {
	for _, e := range events {
		if err := st.handle(sseEvent{Data: []byte(e)}); err != nil {
			if errors.Is(err, errStreamDone) {
				break
			}
			return Message{}, Usage{}, err
		}
	}
	return finish(st.result())
}

func TestAnthropicStreamState(t *testing.T) {
	var deltas []Delta
	text := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	}
	st := &anthropicStream{model: "m", onDelta: func(d Delta) { deltas = append(deltas, d) }}
	msg, u, err := feed(t, st, append(text, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":7}}`, `{"type":"message_stop"}`)...)
	if err != nil || msg.Text() != "partial\n"+CutOffMarker || u.Input != 3 || u.Output != 7 || len(deltas) != 1 {
		t.Errorf("cut off: %q %+v %v %v", msg.Text(), u, err, deltas)
	}
	st = &anthropicStream{model: "m", onDelta: func(Delta) {}}
	if _, _, err := feed(t, st, text...); !errors.Is(err, ErrIncomplete) {
		t.Errorf("no message_stop: %v", err)
	}
	st = &anthropicStream{model: "m", onDelta: func(Delta) {}}
	if _, _, err := feed(t, st, `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, `{"type":"message_stop"}`); !errors.Is(err, errRefused) {
		t.Errorf("refusal: %v", err)
	}
	st = &anthropicStream{model: "m", onDelta: func(Delta) {}}
	if _, _, err := feed(t, st, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`); !Retryable(err) {
		t.Errorf("stream error: %v", err)
	}
}

func TestOpenAIStreamState(t *testing.T) {
	st := &openAIStream{model: "m", onDelta: func(Delta) {}}
	if _, _, err := feed(t, st, `{"choices":[{"delta":{},"finish_reason":"length"}]}`); !errors.Is(err, errEmptyAtMaxToken) {
		t.Errorf("empty at max tokens: %v", err)
	}
	st = &openAIStream{model: "m", onDelta: func(Delta) {}}
	if _, _, err := feed(t, st, `[DONE]`); !errors.Is(err, errEmpty) {
		t.Errorf("empty: %v", err)
	}
	st = &openAIStream{model: "m", onDelta: func(Delta) {}}
	msg, _, err := feed(t, st, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"bash","arguments":"{\"command\":"}}]}}]}`, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":"tool_calls"}]}`)
	if err != nil || len(msg.ToolUses()) != 1 || string(msg.ToolUses()[0].Input) != `{"command":"ls"}` {
		t.Errorf("tool call without [DONE]: %+v %v", msg, err)
	}
}

func TestAnthropicStreamRejectsBadIndex(t *testing.T) {
	for _, idx := range []string{"-1", "1024", "100000000"} {
		for _, ev := range []string{
			`{"type":"content_block_start","index":` + idx + `,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":` + idx + `,"delta":{"type":"text_delta","text":"x"}}`,
		} {
			st := &anthropicStream{model: "m", onDelta: func(Delta) {}}
			if _, _, err := feed(t, st, ev); err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Errorf("index %s: %v", idx, err)
			}
		}
	}
}
