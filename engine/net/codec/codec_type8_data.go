package netCodec

import (
	"fmt"
)

func NewCodec_type8_data() Codec {
	return new(codec_type8_data)
}

type codec_type8_data struct {
}

func (this *codec_type8_data) Encode(cb uint32, t uint32, v []byte) ([]byte, error) {
	tData := make([]byte, 1, len(v)+1)
	tData[0] = uint8(t)
	data := append(tData, v...)
	return data, nil
}

func (this *codec_type8_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	len := len(data)
	if len < 1 {
		return 0, 0, nil, fmt.Errorf("data len is %v, less than 1", len)
	}
	t := uint32(data[0])
	return uint32(0), t, data[1:], nil
}
