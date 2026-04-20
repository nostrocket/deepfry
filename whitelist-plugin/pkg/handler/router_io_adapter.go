package handler

import (
	"bufio"
	"io"
)

// RouterIOAdapter is the JSONL adapter for the router plugin. It differs from
// JSONLIOAdapter only in that its Input returns a RouterInputMsg, which
// preserves the raw event JSON for forwarding to quarantine.
type RouterIOAdapter struct {
	writer *bufio.Writer
}

func NewRouterIOAdapter(w io.Writer) *RouterIOAdapter {
	return &RouterIOAdapter{writer: bufio.NewWriter(w)}
}

func (a *RouterIOAdapter) Input(input []byte) (RouterInputMsg, error) {
	return DeserializeRouterInputMsg(input)
}

func (a *RouterIOAdapter) Output(msg OutputMsg) ([]byte, error) {
	return SerializeOutputMsg(msg)
}

func (a *RouterIOAdapter) Flush() error {
	return a.writer.Flush()
}
