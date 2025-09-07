package handler

type IOAdapter interface {
	Input(input []byte) (InputMsg, error)
	Output(OutputMsg) ([]byte, error)
}

type Handler interface {
	Handle(input InputMsg) (OutputMsg, error)
}
