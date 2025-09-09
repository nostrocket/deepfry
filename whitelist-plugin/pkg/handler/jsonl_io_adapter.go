package handler

import (
	"bufio"
	"io"
)

type JSONLIOAdapter struct {
	writer *bufio.Writer
}

func NewJSONLIOAdapter(w io.Writer) *JSONLIOAdapter {
	return &JSONLIOAdapter{
		writer: bufio.NewWriter(w),
	}
}

func (a *JSONLIOAdapter) Input(input []byte) (InputMsg, error) {
	return DeserializeInputMsg(input)
}

func (a *JSONLIOAdapter) Output(msg OutputMsg) ([]byte, error) {
	return SerializeOutputMsg(msg)
}

func (a *JSONLIOAdapter) Flush() error {
	return a.writer.Flush()
}
