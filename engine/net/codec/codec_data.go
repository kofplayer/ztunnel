package netCodec

func NewCodec_data() Codec {
	return new(codec_data)
}

type codec_data struct {
}

func (this *codec_data) Encode(cb uint32, t uint32, v []byte) ([]byte, error) {
	return v, nil
}

func (this *codec_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	return uint32(0), 0, data, nil
}
