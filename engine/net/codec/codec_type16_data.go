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
	// 同 codec_type8_data：超宽的值必须报错，不能静默截断（报告 L-2）。
	if t > 0xFFFF {
		return nil, fmt.Errorf("codec_type16: msgID %d exceeds 2-byte header (max 65535)", t)
	}
	tData := make([]byte, 2, len(v)+2)
	binary.BigEndian.PutUint16(tData[:2], uint16(t))
	data := append(tData, v...)
	return data, nil
}

func (this *codec_type16_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	n := len(data)
	if n < 2 {
		return 0, 0, nil, fmt.Errorf("data len is %v, less than 2", n)
	}
	t := uint32(binary.BigEndian.Uint16(data[0:2]))
	return uint32(0), t, data[2:], nil
}
