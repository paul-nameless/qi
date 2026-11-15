package llm

import (
	"errors"
	"io"
)

type streamState interface {
	handle(ev sseEvent) error
	result() (Message, Usage, streamEnd)
}

type streamEnd struct {
	complete bool
	cutOff   bool
	refused  bool
}

var (
	errRefused         = errors.New("the model declined to answer (stop_reason: refusal)")
	errEmptyAtMaxToken = errors.New("the response hit max_tokens before producing any text")
	errEmpty           = errors.New("the model returned an empty response")
)

func runStream(body io.ReadCloser, st streamState) (Message, Usage, error) {
	err := readSSE(body, st.handle)
	msg, usage, end := st.result()
	if err != nil && err != errStreamDone {
		return Message{}, usage, streamErr(body, err)
	}
	return finish(msg, usage, end)
}

func finish(msg Message, u Usage, end streamEnd) (Message, Usage, error) {
	switch {
	case !end.complete:
		return Message{}, u, ErrIncomplete
	case end.refused:
		return Message{}, u, errRefused
	case len(msg.ToolUses()) == 0 && msg.Text() == "" && end.cutOff:
		return Message{}, u, errEmptyAtMaxToken
	case len(msg.ToolUses()) == 0 && msg.Text() == "":
		return Message{}, u, errEmpty
	case end.cutOff:
		msg.Blocks = append(msg.Blocks, Block{Type: BlockText, Text: "\n" + CutOffMarker})
	}
	return msg, u, nil
}
