package netCodec

type Codec interface {
	Encode(cb uint32, t uint32, data []byte) ([]byte, error)
	Decode(pkg []byte) (uint32, uint32, []byte, error)
}
