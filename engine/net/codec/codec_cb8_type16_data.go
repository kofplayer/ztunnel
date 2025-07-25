package netCodec

import (
	"encoding/binary"
	"fmt"
)

func NewCodec_cb8_type16_data() Codec {
	return new(codec_cb8_type16_data)
}

type codec_cb8_type16_data struct {
}

func (this *codec_cb8_type16_data) Encode(cb uint32, t uint32, v []byte) ([]byte, error) {
	tData := make([]byte, 3, len(v)+3)
	tData[0] = uint8(cb)
	binary.BigEndian.PutUint16(tData[1:], uint16(t))
	data := append(tData, v...)
	return data, nil
}

func (this *codec_cb8_type16_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	l := len(data)
	if l < 3 {
		return 0, 0, nil, fmt.Errorf("data len is %v, less than 3", l)
	}
	cb := uint32(data[0])
	t := uint32(binary.BigEndian.Uint16(data[1:3]))
	return cb, t, data[3:], nil
}
