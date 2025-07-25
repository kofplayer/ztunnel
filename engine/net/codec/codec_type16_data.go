package netCodec

import (
	"encoding/binary"
	"fmt"
)

func NewCodec_type16_data() Codec {
	return new(codec_type16_data)
}

type codec_type16_data struct {
}

func (this *codec_type16_data) Encode(cb uint32, t uint32, v []byte) ([]byte, error) {
	tData := make([]byte, 2, len(v)+2)
	binary.BigEndian.PutUint16(tData[:2], uint16(t))
	data := append(tData, v...)
	return data, nil
}

func (this *codec_type16_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	len := len(data)
	if len < 2 {
		return 0, 0, nil, fmt.Errorf("data len is %v, less than 2", len)
	}
	t := uint32(binary.BigEndian.Uint16(data[0:2]))
	return uint32(0), t, data[2:], nil
}
